package handler

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/chatconfig"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestChatContextReportScopeAndPersistence(t *testing.T) {
	_, sessionID, taskID, daemonID := planningFixtureRows(t, "claude")
	// Use a private synchronous bus to inspect the actual producer event.
	h := *testHandler
	h.Bus = events.New()
	var updates []events.Event
	h.Bus.Subscribe(protocol.EventChatSessionUpdated, func(e events.Event) { updates = append(updates, e) })
	report := func(body any, status int) {
		t.Helper()
		req := withURLParam(newDaemonTokenRequest("POST", "/context", body, testWorkspaceID, daemonID), "taskId", taskID)
		testutil.Call(t, h.ReportChatContext, req).Want(status)
	}
	report(map[string]any{"usage": map[string]int{"used": 420000, "window": 1000000}}, 200)
	report(map[string]any{"compaction": map[string]string{"status": "started"}}, 200)
	report(map[string]any{"compaction": map[string]string{"status": "completed"}}, 200)
	if len(updates) != 3 {
		t.Fatalf("updates = %d, want usage/start/completed", len(updates))
	}
	for _, e := range updates {
		if e.ActorType != "member" || e.ActorID != testUserID || e.WorkspaceID != testWorkspaceID || e.ChatSessionID != sessionID {
			t.Fatalf("context event must route privately to the creator: %+v", e)
		}
		payload, ok := e.Payload.(map[string]any)
		if !ok || payload["context_changed"] != true || payload["chat_session_id"] != sessionID {
			t.Fatalf("missing context invalidation: %+v", e.Payload)
		}
	}
	var session ChatSessionResponse
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest("GET", "/chat", nil), "sessionId", sessionID))
	testutil.Call(t, testHandler.GetChatSession, req).Want(200).JSON(&session)
	var state map[string]json.RawMessage
	if json.Unmarshal(session.ContextState, &state) != nil || len(state["usage"]) == 0 || len(state["compaction"]) == 0 {
		t.Fatalf("lost state %s", session.ContextState)
	}
	report(map[string]any{"usage": map[string]int{"used": 4, "window": 0}}, 400)
	report(json.RawMessage(`null`), 400)
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed' WHERE id=$1", taskID)
	report(map[string]any{"usage": nil}, 409)
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='running', chat_session_id=NULL, issue_id=$2 WHERE id=$1", taskID, dbfx.Issue(t, "No context telemetry for issues"))
	report(map[string]any{"usage": nil}, 409)
}

func TestChatCompactQueuesClaimsWithoutInputAndSuppressesReply(t *testing.T) {
	ctx := context.Background()
	agentID, sessionID, firstTask, daemonID := planningFixtureRows(t, "codex")
	session, _ := testHandler.Queries.GetChatSession(ctx, parseUUID(sessionID))
	carrier, _ := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	sent, err := testHandler.TaskService.SendDirectChatAction(ctx, session, carrier, parseUUID(testUserID), "compact")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", uuidToString(sent.Task.ID))
	if !sent.Queued || chatconfig.Action(sent.Task.Context) != "compact" {
		t.Fatalf("not a queued action %+v", sent)
	}
	if dbfx.Count(t, "SELECT count(*) FROM chat_message WHERE task_id=$1", uuidToString(sent.Task.ID)) != 0 {
		t.Fatal("control action wrote a normal message")
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed' WHERE id=$1", firstTask)
	dbfx.Exec(t, "UPDATE chat_session SET session_id='native', runtime_id=$2 WHERE id=$1", sessionID, uuidToString(carrier.RuntimeID))
	previous, _ := json.Marshal(map[string]any{"runtime_id": uuidToString(carrier.RuntimeID), "usage": map[string]any{"used": 22657, "window": 1000000, "model": "session-model"}})
	dbfx.Exec(t, "UPDATE chat_session SET context_state=$2 WHERE id=$1", sessionID, previous)
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='dispatched' WHERE id=$1", uuidToString(sent.Task.ID))
	task, _ := testHandler.Queries.GetAgentTask(ctx, sent.Task.ID)
	rt, _ := testHandler.Queries.GetAgentRuntime(ctx, carrier.RuntimeID)
	req := newDaemonTokenRequest("POST", "/claim", nil, testWorkspaceID, daemonID)
	req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityChatContextV1)
	response, _, _, _, _, failure := testHandler.buildClaimedTaskResponse(req, &task, rt, uuidToString(rt.ID), testWorkspaceID)
	if failure != nil || response.ChatAction != "compact" || response.PriorSessionID != "native" {
		t.Fatalf("claim %+v failure %+v", response, failure)
	}
	if response.PriorContextUsage == nil || response.PriorContextUsage.Window != 1000000 || response.PriorContextUsage.Model != "session-model" {
		t.Fatalf("lost previous native context at claim: %+v", response.PriorContextUsage)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='running' WHERE id=$1", uuidToString(task.ID))
	_, err = testHandler.TaskService.CompleteTask(ctx, task.ID, []byte(`{"output":"must not be published"}`), "native", "", "", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if dbfx.Count(t, "SELECT count(*) FROM chat_message WHERE task_id=$1", uuidToString(task.ID)) != 0 {
		t.Fatal("compact created assistant reply")
	}
}

func TestProjectDirectorySyncHeartbeatFlow(t *testing.T) {
	ctx := context.Background()
	daemonID := "directory-sync-daemon"
	runtimeID := dbfx.Runtime(t, "Directory sync", testutil.Cols{"provider": "claude", "daemon_id": daemonID, "runtime_mode": "local"})
	agentID := dbfx.Agent(t, "Sync agent", runtimeID)
	carrier, _ := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	projectID := dbfx.Project(t, "Directory sync")
	dbfx.Exec(t, `UPDATE agent_runtime SET metadata='{"capabilities":["chat-context-v1"]}' WHERE id=$1`, uuidToString(carrier.RuntimeID))
	dbfx.Insert(t, "project_resource", testutil.Cols{"project_id": projectID, "workspace_id": testWorkspaceID, "resource_type": "local_directory", "resource_ref": json.RawMessage(`{"local_path":"/fixture/repo","daemon_id":"` + daemonID + `"}`), "created_by": testUserID})
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest("POST", "/sync", map[string]any{"agent_id": agentID, "force": true}), "id", projectID))
	var request struct {
		ID string `json:"id"`
	}
	testutil.Call(t, testHandler.InitiateProjectDirectorySync, req).Want(202).JSON(&request)
	dbfx.Cleanup(t, "DELETE FROM chat_directory_sync WHERE id=$1", request.ID)
	ack, _, err := testHandler.processHeartbeat(ctx, uuidToString(carrier.RuntimeID), false)
	if err != nil || len(ack.PendingDirectorySync) != 1 {
		t.Fatalf("heartbeat %+v %v", ack, err)
	}
	if !ack.PendingDirectorySync[0].Force {
		t.Fatal("heartbeat lost force confirmation")
	}
	ack, _, err = testHandler.processHeartbeat(ctx, uuidToString(carrier.RuntimeID), false)
	if err != nil || len(ack.PendingDirectorySync) != 0 {
		t.Fatal("request claimed twice")
	}
	report := withURLParam(newDaemonTokenRequest("POST", "/sync", map[string]any{"id": request.ID, "result": map[string]any{"status": "updated", "updated": 3, "ahead": 0, "behind": 0}}, testWorkspaceID, daemonID), "runtimeId", uuidToString(carrier.RuntimeID))
	testutil.Call(t, testHandler.ReportChatDirectorySync, report).Want(200)
	get := withChatTestWorkspaceCtx(t, withURLParams(newRequest("GET", "/sync", nil), "id", projectID, "syncId", request.ID))
	var result map[string]any
	testutil.Call(t, testHandler.GetProjectDirectorySync, get).Want(200).JSON(&result)
	assertChatContextContract(t, "sync", result, "id")
	// Result visibility is scoped to both project and the initiating member.
	otherProject := dbfx.Project(t, "Other sync project")
	wrongProject := withChatTestWorkspaceCtx(t, withURLParams(newRequest("GET", "/sync", nil), "id", otherProject, "syncId", request.ID))
	testutil.Call(t, testHandler.GetProjectDirectorySync, wrongProject).Want(404)
	dbfx.Exec(t, "UPDATE chat_directory_sync SET requester_id=$2 WHERE id=$1", request.ID, projectID)
	testutil.Call(t, testHandler.GetProjectDirectorySync, get).Want(404)
	dbfx.Exec(t, "UPDATE chat_directory_sync SET requester_id=$2 WHERE id=$1", request.ID, testUserID)
	remove := withChatTestWorkspaceCtx(t, withURLParam(newRequest("DELETE", "/project", nil), "id", projectID))
	testutil.Call(t, testHandler.DeleteProject, remove).Want(204)
	if dbfx.Count(t, "SELECT count(*) FROM chat_directory_sync WHERE id=$1", request.ID) != 0 {
		t.Fatal("project deletion left a sync request")
	}
}

func TestChatCompactHTTPResponseContract(t *testing.T) {
	_, sessionID, _, _ := planningFixtureRows(t, "codex")
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest("POST", "/messages", map[string]any{"action": "compact", "content": ""}), "sessionId", sessionID))
	var response map[string]any
	testutil.Call(t, testHandler.SendChatMessage, req).Want(201).JSON(&response)
	taskID, ok := response["task_id"].(string)
	if !ok || taskID == "" {
		t.Fatalf("missing queued task id: %+v", response)
	}
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", taskID)
	if dbfx.Count(t, "SELECT count(*) FROM chat_message WHERE task_id=$1", taskID) != 0 {
		t.Fatal("compact wrote a chat message")
	}
	assertChatContextContract(t, "compact", response, "task_id")
}

// The API client tests consume this same fixture. Normalize only generated IDs
// and timestamps so incompatible wire types (including base64 JSON) fail here.
func assertChatContextContract(t *testing.T, name string, got map[string]any, idKey string) {
	t.Helper()
	data, err := os.ReadFile("testdata/chat-context-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]map[string]any
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	want := fixtures[name]
	if id, ok := got[idKey].(string); !ok || id == "" {
		t.Fatalf("missing %s: %+v", idKey, got)
	}
	stamp, ok := got["created_at"].(string)
	if !ok {
		t.Fatalf("missing timestamp: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		t.Fatal(err)
	}
	got[idKey], got["created_at"] = want[idKey], want["created_at"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s wire contract: got %#v want %#v", name, got, want)
	}
}

func TestChatCompactFailurePreservesUsageAndPublishesStatus(t *testing.T) {
	ctx := context.Background()
	_, sessionID, taskID, daemonID := planningFixtureRows(t, "claude")
	dbfx.Exec(t, `UPDATE agent_task_queue SET context='{"chat_action":"compact"}' WHERE id=$1`, taskID)
	report := func(body any) {
		req := withURLParam(newDaemonTokenRequest("POST", "/context", body, testWorkspaceID, daemonID), "taskId", taskID)
		testutil.Call(t, testHandler.ReportChatContext, req).Want(200)
	}
	report(map[string]any{"usage": map[string]any{"used": 22657, "window": 1000000, "model": "native"}})
	report(map[string]any{"usage": nil})
	service := testHandler.TaskService
	previousBus := service.Bus
	service.Bus = events.New()
	t.Cleanup(func() { service.Bus = previousBus })
	var updates []events.Event
	service.Bus.Subscribe(protocol.EventChatSessionUpdated, func(e events.Event) { updates = append(updates, e) })
	if _, err := service.FailTask(ctx, parseUUID(taskID), "No conversation found", "", "", "", "agent_error", false, "", ""); err != nil {
		t.Fatal(err)
	}
	session, err := testHandler.Queries.GetChatSession(ctx, parseUUID(sessionID))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Usage      struct{ Used, Window int64 }
		Compaction struct{ Status, Error string }
	}
	if err := json.Unmarshal(session.ContextState, &state); err != nil {
		t.Fatal(err)
	}
	if state.Usage.Used != 22657 || state.Usage.Window != 1000000 || state.Compaction.Status != "failed" || state.Compaction.Error != "No conversation found" {
		t.Fatalf("state %s", session.ContextState)
	}
	if len(updates) != 1 || updates[0].ActorID != testUserID {
		t.Fatalf("missing creator notification: %+v", updates)
	}
	if dbfx.Count(t, "SELECT count(*) FROM chat_message WHERE task_id=$1", taskID) != 0 {
		t.Fatal("failure wrote an assistant reply")
	}
}

func TestChatDirectorySyncMigrationCascadesAndRollback(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// Shadow parent tables on this connection only; never mutate public tables.
	for _, table := range []string{"chat_session", "workspace", "agent_runtime"} {
		if _, err := tx.Exec(ctx, "CREATE TEMP TABLE "+table+" (id uuid PRIMARY KEY) ON COMMIT DROP"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path = pg_temp, public"); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../migrations/571_chat_directory_sync.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/571_chat_directory_sync.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	var gone bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('pg_temp.chat_directory_sync') IS NULL").Scan(&gone); err != nil || !gone {
		t.Fatalf("down: %v, %v", gone, err)
	}
	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{"chat_session", "workspace", "agent_runtime"} {
		for _, table := range []string{"chat_session", "workspace", "agent_runtime"} {
			if _, err := tx.Exec(ctx, "INSERT INTO "+table+" VALUES ($1) ON CONFLICT DO NOTHING", testUserID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat_directory_sync (id,chat_session_id,workspace_id,runtime_id,resource_ref) VALUES ($1,$1,$1,$1,'{}')`, testUserID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM "+parent+" WHERE id=$1", testUserID); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM chat_directory_sync").Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s left %d sync records: %v", parent, count, err)
		}
	}
}

func TestProjectDirectorySyncAccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{
		{"foreign project", 404},
		{"foreign agent", 404},
		{"foreign runtime", 404},
		{"private agent", 403},
		{"private runtime", 404},
		{"old daemon", 409},
		{"other daemon directory", 409},
		{"missing runtime", 409},
		{"invalid agent", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projectID := dbfx.Project(t, "Sync access")
			runtimeID := dbfx.Runtime(t, "Sync access", testutil.Cols{"daemon_id": "sync-access-daemon", "metadata": json.RawMessage(`{"capabilities":["chat-context-v1"]}`)})
			agentID := dbfx.Agent(t, "Sync access", runtimeID)
			refDaemon := "sync-access-daemon"
			switch tc.name {
			case "foreign project", "foreign agent", "foreign runtime":
				workspaceID := dbfx.Workspace(t, "Other sync workspace", "sync-workspace-"+projectID)
				if tc.name == "foreign project" {
					dbfx.Exec(t, "UPDATE project SET workspace_id=$2 WHERE id=$1", projectID, workspaceID)
				} else if tc.name == "foreign agent" {
					dbfx.Exec(t, "UPDATE agent SET workspace_id=$2 WHERE id=$1", agentID, workspaceID)
				} else {
					dbfx.Member(t, workspaceID, testUserID, "member")
					dbfx.Exec(t, "UPDATE agent_runtime SET workspace_id=$2 WHERE id=$1", runtimeID, workspaceID)
				}
			case "private agent", "private runtime":
				otherUser := dbfx.User(t, "Other sync owner", projectID+"@sync.example")
				if tc.name == "private agent" {
					dbfx.Exec(t, "UPDATE agent SET owner_id=$2 WHERE id=$1", agentID, otherUser)
				} else {
					dbfx.Exec(t, "UPDATE agent_runtime SET owner_id=$2 WHERE id=$1", runtimeID, otherUser)
				}
			case "old daemon":
				dbfx.Exec(t, "UPDATE agent_runtime SET metadata='{}' WHERE id=$1", runtimeID)
			case "other daemon directory":
				refDaemon = "different-machine"
			case "missing runtime":
				dbfx.Exec(t, "UPDATE agent SET runtime_id=NULL WHERE id=$1", agentID)
			case "invalid agent":
				agentID = "invalid"
			}
			ref, _ := json.Marshal(map[string]string{"local_path": "/fixture/repo", "daemon_id": refDaemon})
			dbfx.Insert(t, "project_resource", testutil.Cols{"project_id": projectID, "workspace_id": testWorkspaceID, "resource_type": "local_directory", "resource_ref": json.RawMessage(ref), "created_by": testUserID})
			req := withChatTestWorkspaceCtx(t, withURLParam(newRequest("POST", "/sync", map[string]any{"agent_id": agentID, "force": true}), "id", projectID))
			testutil.Call(t, testHandler.InitiateProjectDirectorySync, req).Want(tc.want)
			if dbfx.Count(t, "SELECT count(*) FROM chat_directory_sync WHERE project_id=$1", projectID) != 0 {
				t.Fatal("rejected request reached daemon queue")
			}
		})
	}
}

func TestProjectDirectorySyncMigrationAndRollback(t *testing.T) {
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// The legacy queue is isolated on this connection, including its parents.
	for _, ddl := range []string{
		"CREATE TEMP TABLE chat_session (id uuid PRIMARY KEY, project_id uuid, creator_id uuid NOT NULL) ON COMMIT DROP",
		"CREATE TEMP TABLE workspace (id uuid PRIMARY KEY) ON COMMIT DROP",
		"CREATE TEMP TABLE agent_runtime (id uuid PRIMARY KEY) ON COMMIT DROP",
		"SET LOCAL search_path = pg_temp, public",
	} {
		if _, err := tx.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(name string) {
		t.Helper()
		ddl, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(ddl)); err != nil {
			t.Fatal(err)
		}
	}
	apply("571_chat_directory_sync.up.sql")
	for _, query := range []string{
		"INSERT INTO chat_session VALUES ($1, $1, $1)",
		"INSERT INTO workspace VALUES ($1)",
		"INSERT INTO agent_runtime VALUES ($1)",
		"INSERT INTO chat_directory_sync (id,chat_session_id,workspace_id,runtime_id,resource_ref) VALUES ($1,$1,$1,$1,'{}')",
	} {
		if _, err := tx.Exec(ctx, query, testUserID); err != nil {
			t.Fatal(err)
		}
	}
	apply("573_project_directory_sync.up.sql")
	var projectID, requesterID string
	var force bool
	if err := tx.QueryRow(ctx, "SELECT project_id, requester_id, force FROM chat_directory_sync").Scan(&projectID, &requesterID, &force); err != nil {
		t.Fatal(err)
	}
	if projectID != testUserID || requesterID != testUserID || force {
		t.Fatalf("unexpected migrated request: %s %s %v", projectID, requesterID, force)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM chat_session"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM chat_directory_sync").Scan(&count); err != nil || count != 1 {
		t.Fatalf("project request still tied to session: count=%d err=%v", count, err)
	}
	apply("573_project_directory_sync.down.sql")
	if err := tx.QueryRow(ctx, "SELECT count(chat_session_id) FROM chat_directory_sync").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback: count=%d err=%v", count, err)
	}
	apply("573_project_directory_sync.up.sql")
}
