package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/planning"
)

func TestPlanningCardsHTTPObjects(t *testing.T) {
	_, sessionID, taskID, daemonID := planningFixtureRows(t, "claude")
	card := planning.Card{SchemaVersion: 1, Kind: "plan", Title: "Plan", Continuation: "new_turn", Source: planning.Source{Provider: "claude", ConversationID: "session", ItemID: "raw-json"}, Markdown: "Actual plan"}
	req := withURLParam(newDaemonTokenRequest("POST", "/cards", card, testWorkspaceID, daemonID), "taskId", taskID)
	rec := httptest.NewRecorder()
	testHandler.ReportChatCard(rec, req)
	var raw map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &raw) != nil {
		t.Fatalf("report: %s", rec.Body.String())
	}
	payload, ok := raw["payload"].(map[string]any)
	if !ok || payload["markdown"] != "Actual plan" {
		t.Fatalf("payload is not an object: %s", rec.Body.String())
	}
	dbfx.Exec(t, `UPDATE chat_card SET response='{"action":"approve","feedback":"Add tests"}' WHERE task_id=$1`, taskID)
	req = withChatTestWorkspaceCtx(t, withURLParam(newRequest("GET", "/cards", nil), "sessionId", sessionID))
	rec = httptest.NewRecorder()
	testHandler.ListChatCards(rec, req)
	var rows []map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rows) != nil || len(rows) != 1 {
		t.Fatalf("list: %s", rec.Body.String())
	}
	if _, ok := rows[0]["payload"].(map[string]any); !ok {
		t.Fatal("list payload is not an object")
	}
	if response, ok := rows[0]["response"].(map[string]any); !ok || response["feedback"] != "Add tests" {
		t.Fatalf("response is not an object: %+v", rows)
	}
}

func TestPlanningMentionPrivacyAndScope(t *testing.T) {
	agentID, sessionID, _, _ := planningFixtureRows(t, "codex")
	issueID := dbfx.Issue(t, "Mention boundary")
	dbfx.Exec(t, "INSERT INTO issue_chat_session (workspace_id, issue_id, chat_session_id) VALUES ($1,$2,$3)", testWorkspaceID, issueID, sessionID)
	issue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	bob := createWorkspaceMemberUser(t, "Planning Bob", "planning-bob@multica.test")
	bobComment := dbfx.Comment(t, issueID, "Bob mention", testutil.Cols{"author_id": bob})
	row, err := testHandler.TaskService.EnqueueTaskForMention(context.Background(), issue, parseUUID(agentID), parseUUID(bobComment), service.OriginNamed)
	if err != nil || row.ChatSessionID.Valid {
		t.Fatalf("non-creator entered private chat: %+v %v", row, err)
	}
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issueID)
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed' WHERE id=$1", uuidToString(row.ID))
	ownerComment := dbfx.Comment(t, issueID, "Owner mention")
	row, err = testHandler.TaskService.EnqueueTaskForMention(context.Background(), issue, parseUUID(agentID), parseUUID(ownerComment), service.OriginNamed)
	if err != nil || uuidToString(row.ChatSessionID) != sessionID {
		t.Fatalf("owner did not route: %+v %v", row, err)
	}
	bobReply := dbfx.Comment(t, issueID, "Bob reply in the same thread", testutil.Cols{"author_id": bob, "parent_id": ownerComment})
	planner, _ := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if got := testHandler.mergeCommentIntoPendingTask(context.Background(), issue, commentAgentTrigger{Agent: planner, Source: commentTriggerSourceMentionAgent}, parseUUID(bobReply), pgtype.Text{}); got != commentMergeNoPendingTask {
		t.Fatalf("Bob merged into private chat: %v", got)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed', session_id='private-native', work_dir='/private' WHERE id=$1", uuidToString(row.ID))
	if last, err := testHandler.Queries.GetLastTaskSession(context.Background(), db.GetLastTaskSessionParams{AgentID: parseUUID(agentID), IssueID: issue.ID}); err == nil {
		t.Fatalf("normal issue resumed private session: %+v", last)
	}
	// Linking request context must not redirect an assignment to a squad leader.
	squadID := dbfx.Squad(t, "Planning squad", agentID)
	row, err = testHandler.TaskService.EnqueueTaskForSquadLeader(service.WithPlanningChat(context.Background(), sessionID, false), issue, parseUUID(agentID), parseUUID(squadID), pgtype.UUID{}, service.OriginNamed)
	if err != nil || row.ChatSessionID.Valid {
		t.Fatalf("squad inherited linking context: %+v %v", row, err)
	}
}

func TestPlanningAutomaticLinkRequiresTaskToken(t *testing.T) {
	agentID, _, taskID, _ := planningFixtureRows(t, "claude")
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{"title": "Untrusted headers"})
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	var issue IssueResponse
	testutil.Call(t, testHandler.CreateIssue, req).Want(201).JSON(&issue)
	dbfx.Cleanup(t, "DELETE FROM issue WHERE id=$1", issue.ID)
	if dbfx.Count(t, "SELECT count(*) FROM issue_chat_session WHERE issue_id=$1", issue.ID) != 0 {
		t.Fatal("untrusted headers established a planning link")
	}
}

func TestPlanningClaimNeverFallsBackToIssueSession(t *testing.T) {
	for _, withChatPointer := range []bool{false, true} {
		t.Run(fmt.Sprint(withChatPointer), func(t *testing.T) {
			agentID, sessionID, taskID, daemonID := planningFixtureRows(t, "claude")
			issueID := dbfx.Issue(t, "Resume scope")
			var runtimeID string
			dbfx.QueryRow(t, "SELECT runtime_id FROM agent WHERE id=$1", agentID).Scan(&runtimeID)
			dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": "completed", "session_id": "ordinary-native", "work_dir": "/ordinary"})
			if withChatPointer {
				dbfx.Exec(t, "UPDATE chat_session SET session_id='chat-native', work_dir='/chat', runtime_id=$2 WHERE id=$1", sessionID, runtimeID)
			}
			dbfx.Exec(t, "UPDATE agent_task_queue SET issue_id=$2 WHERE id=$1", taskID, issueID)
			task, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(taskID))
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), task.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			req := newDaemonTokenRequest("POST", "/claim", nil, testWorkspaceID, daemonID)
			resp, _, _, _, _, failure := testHandler.buildClaimedTaskResponse(req, &task, runtime, runtimeID, testWorkspaceID)
			if failure != nil {
				t.Fatalf("claim: %+v", failure)
			}
			wantSession, wantDir := "", ""
			if withChatPointer {
				wantSession, wantDir = "chat-native", "/chat"
			}
			if resp.PriorSessionID != wantSession || resp.PriorWorkDir != wantDir {
				t.Fatalf("claim crossed scope: %q %q", resp.PriorSessionID, resp.PriorWorkDir)
			}
		})
	}
}

func TestPlanningMixedTaskCancellationBoundaries(t *testing.T) {
	agentID, sessionID, chatTaskID, _ := planningFixtureRows(t, "claude")
	dbfx.Exec(t, "UPDATE agent SET visibility='workspace', permission_mode='public_to' WHERE id=$1", agentID)
	dbfx.Exec(t, "INSERT INTO agent_invocation_target (agent_id, target_type, target_id) VALUES ($1, 'workspace', $2)", agentID, testWorkspaceID)
	dbfx.Cleanup(t, "DELETE FROM agent_invocation_target WHERE agent_id=$1", agentID)
	issueID := dbfx.Issue(t, "Cancellation boundary")
	var runtimeID string
	dbfx.QueryRow(t, "SELECT runtime_id FROM agent WHERE id=$1", agentID).Scan(&runtimeID)
	mixedID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "chat_session_id": sessionID})
	dbfx.Cleanup(t, "DELETE FROM chat_message WHERE chat_session_id=$1", sessionID)
	// Clear queued chat messages cannot silently cancel an issue reply.
	if err := testHandler.TaskService.CancelQueuedChatTasks(context.Background(), parseUUID(sessionID), parseUUID(agentID)); err != nil {
		t.Fatal(err)
	}
	if taskStatus(t, mixedID) != "queued" {
		t.Fatal("clear queue cancelled issue reply")
	}
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest("PATCH", "/archive", map[string]any{"archived": true}), "sessionId", sessionID))
	testutil.Call(t, testHandler.SetChatSessionArchived, req).Want(409)
	req = withChatTestWorkspaceCtx(t, withURLParam(newRequest("DELETE", "/chat", nil), "sessionId", sessionID))
	testutil.Call(t, testHandler.DeleteChatSession, req).Want(409)
	// Issue-side Stop follows issue/agent access and exposes no private draft.
	bob := createWorkspaceMemberUser(t, "Planning Stop Bob", "planning-stop-bob@multica.test")
	rec := httptest.NewRecorder()
	testHandler.CancelTaskByUser(rec, cancelTaskByUserRequest(t, bob, mixedID))
	if rec.Code != 200 {
		t.Fatalf("issue stop: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "cancelled_chat_message") {
		t.Fatalf("private draft exposed: %s", rec.Body.String())
	}
	if taskStatus(t, mixedID) != "cancelled" || taskStatus(t, chatTaskID) != "running" {
		t.Fatal("wrong task cancelled")
	}
}

func planningFixtureRows(t *testing.T, provider string) (agentID, sessionID, taskID, daemonID string) {
	t.Helper()
	if testHandler == nil {
		t.Skip("database not available")
	}
	daemonID = "planning-daemon-" + provider
	runtimeID := dbfx.Runtime(t, "Planning "+provider, testutil.Cols{"provider": provider, "daemon_id": daemonID, "runtime_mode": "local"})
	agentID = dbfx.Agent(t, "Planner", runtimeID)
	sessionID = dbfx.Insert(t, "chat_session", testutil.Cols{"workspace_id": testWorkspaceID, "creator_id": testUserID, "agent_id": agentID, "title": "Planning", "explicitly_created_at": testutil.Raw("now()"), "plan_mode": true})
	taskID = dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "chat_session_id": sessionID, "status": "running", "originator_user_id": testUserID, "accountable_user_id": testUserID})
	dbfx.Cleanup(t, "DELETE FROM chat_card WHERE chat_session_id=$1", sessionID)
	dbfx.Cleanup(t, "DELETE FROM issue_chat_session WHERE chat_session_id=$1", sessionID)
	return
}

func postPlanningCard(t *testing.T, taskID, daemonID string, card planning.Card) ChatCardResponse {
	t.Helper()
	var saved ChatCardResponse
	req := withURLParam(newDaemonTokenRequest("POST", "/cards", card, testWorkspaceID, daemonID), "taskId", taskID)
	testutil.Call(t, testHandler.ReportChatCard, req).Want(200).JSON(&saved)
	return saved
}

func TestPlanningCardPersistenceDecisionsAndReplay(t *testing.T) {
	_, sessionID, taskID, daemonID := planningFixtureRows(t, "claude")
	card := planning.Card{SchemaVersion: 1, Kind: "plan", Title: "Plan", Continuation: "new_turn", Source: planning.Source{Provider: "claude", ConversationID: "native-session", ItemID: "p1"}, Markdown: "# Implement later"}
	first := postPlanningCard(t, taskID, daemonID, card)
	if replay := postPlanningCard(t, taskID, daemonID, card); replay.ID != first.ID {
		t.Fatal("duplicate card")
	}
	card.Source.ItemID = "p2"
	card.Markdown = "# Revised"
	second := postPlanningCard(t, taskID, daemonID, card)
	postPlanningCard(t, taskID, daemonID, planning.Card{SchemaVersion: 1, Kind: "plan", Title: "Plan", Continuation: "new_turn", Source: planning.Source{Provider: "claude", ConversationID: "native-session", ItemID: "p1"}, Markdown: "old replay"})
	var oldStatus, newStatus string
	dbfx.QueryRow(t, "SELECT status FROM chat_card WHERE id=$1", uuidToString(first.ID)).Scan(&oldStatus)
	dbfx.QueryRow(t, "SELECT status FROM chat_card WHERE id=$1", uuidToString(second.ID)).Scan(&newStatus)
	if oldStatus != "superseded" || newStatus != "pending" {
		t.Fatalf("%s %s", oldStatus, newStatus)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed' WHERE id=$1", taskID)
	send := func(action string, status int) SendChatMessageResponse {
		req := withChatTestWorkspaceCtx(t, withURLParam(newRequest("POST", "/messages", map[string]any{"card_decision": planning.Decision{CardID: uuidToString(second.ID), Action: action}}), "sessionId", sessionID))
		var out SendChatMessageResponse
		r := testutil.Call(t, testHandler.SendChatMessage, req).Want(status)
		if status == 201 {
			r.JSON(&out)
		}
		return out
	}
	result := send("approve", 201)
	send("approve", 409)
	var mode bool
	var content string
	dbfx.QueryRow(t, "SELECT plan_mode FROM chat_session WHERE id=$1", sessionID).Scan(&mode)
	dbfx.QueryRow(t, "SELECT content FROM chat_message WHERE task_id=$1", result.TaskID).Scan(&content)
	if mode || !strings.Contains(content, "ONLY write issues; do not implement") {
		t.Fatalf("mode=%v content=%s", mode, content)
	}
	dbfx.Cleanup(t, "DELETE FROM chat_message WHERE chat_session_id=$1", sessionID)
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE chat_session_id=$1", sessionID)
}

func TestPlanningAutomaticLinksAndMentionRouting(t *testing.T) {
	agentID, sessionID, taskID, _ := planningFixtureRows(t, "codex")
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{"title": "Planned issue"})
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	req.Header.Set("X-Actor-Source", "task_token")
	var issue IssueResponse
	testutil.Call(t, testHandler.CreateIssue, req).Want(201).JSON(&issue)
	dbfx.Cleanup(t, "DELETE FROM issue WHERE id=$1", issue.ID)
	if n := dbfx.Count(t, "SELECT count(*) FROM issue_chat_session WHERE issue_id=$1 AND chat_session_id=$2", issue.ID, sessionID); n != 1 {
		t.Fatal("missing automatic link")
	}
	req = withURLParam(newRequest("PATCH", "/api/issues/"+issue.ID, map[string]any{"description": "Revised plan"}), "id", issue.ID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	req.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.UpdateIssue, req).Want(200)
	if n := dbfx.Count(t, "SELECT count(*) FROM issue_chat_session WHERE issue_id=$1", issue.ID); n != 1 {
		t.Fatal("update duplicated link")
	}
	commentID := dbfx.Comment(t, issue.ID, "Please update the plan")
	row, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(issue.ID))
	if err != nil {
		t.Fatal(err)
	}
	mention, err := testHandler.TaskService.EnqueueTaskForMention(context.Background(), row, parseUUID(agentID), parseUUID(commentID), service.OriginNamed)
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issue.ID)
	if uuidToString(mention.ChatSessionID) != sessionID || mention.IssueID != row.ID {
		t.Fatalf("wrong destination: %+v", mention)
	}

	// Coalescing must not convert a normal queued task into a private chat task.
	dbfx.Exec(t, "UPDATE agent_task_queue SET chat_session_id=NULL WHERE id=$1", uuidToString(mention.ID))
	planner, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatal(err)
	}
	merged := testHandler.mergeCommentIntoPendingTask(context.Background(), row, commentAgentTrigger{Agent: planner, Source: commentTriggerSourceMentionAgent}, parseUUID(commentID), pgtype.Text{})
	if merged != commentMergeNoPendingTask {
		t.Fatalf("merge crossed conversation boundary: %v", merged)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET chat_session_id=$2 WHERE id=$1", uuidToString(mention.ID), sessionID)
	merged = testHandler.mergeCommentIntoPendingTask(context.Background(), row, commentAgentTrigger{Agent: planner, Source: commentTriggerSourceMentionAgent}, parseUUID(commentID), pgtype.Text{})
	if merged != commentMergeSucceeded {
		t.Fatalf("same conversation merge: %v", merged)
	}
	// Manual unlink and relink use the same private-chat ownership gate.
	for _, unlink := range []bool{true, false} {
		req = withChatTestWorkspaceCtx(t, withURLParam(newRequest("PUT", "/issue", map[string]any{"planning_chat_session_id": sessionID, "unlink_planning_chat": unlink}), "id", issue.ID))
		testutil.Call(t, testHandler.UpdateIssue, req).Want(200)
		want := 1
		if unlink {
			want = 0
		}
		if n := dbfx.Count(t, "SELECT count(*) FROM issue_chat_session WHERE issue_id=$1", issue.ID); n != want {
			t.Fatalf("manual link: %d", n)
		}
	}
	otherID := dbfx.Agent(t, "Other planner", handlerTestRuntimeID(t))
	other, err := testHandler.TaskService.EnqueueTaskForMention(context.Background(), row, parseUUID(otherID), parseUUID(commentID), service.OriginNamed)
	if err != nil {
		t.Fatal(err)
	}
	if other.ChatSessionID.Valid {
		t.Fatal("unrelated agent redirected")
	}
}

func TestPlanningUnsupportedRuntimeAndInvalidScope(t *testing.T) {
	agentID, sessionID, _, _ := planningFixtureRows(t, "codex")
	dbfx.Exec(t, "UPDATE agent_runtime SET provider='pi' WHERE id=(SELECT runtime_id FROM agent WHERE id=$1)", agentID)
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest("PATCH", "/chat", map[string]any{"plan_mode": true}), "sessionId", sessionID))
	testutil.Call(t, testHandler.UpdateChatSession, req).Want(http.StatusBadRequest)
	req = withChatTestWorkspaceCtx(t, withURLParam(newRequest("POST", "/messages", map[string]any{"content": "plan it"}), "sessionId", sessionID))
	testutil.Call(t, testHandler.SendChatMessage, req).Want(http.StatusBadRequest)
	if _, err := validateLocalDirectoryRef(json.RawMessage(`{"local_path":"/repo","daemon_id":"d","scope":"invalid"}`)); err == nil {
		t.Fatal("unknown scope accepted")
	}
}

func TestPlanningMentionFallsBackWhenChatUnavailable(t *testing.T) {
	for _, reason := range []string{"archived", "unsupported"} {
		t.Run(reason, func(t *testing.T) {
			agentID, sessionID, _, _ := planningFixtureRows(t, "claude")
			issueID := dbfx.Issue(t, "Fallback mention")
			dbfx.Exec(t, "INSERT INTO issue_chat_session (workspace_id, issue_id, chat_session_id) VALUES ($1,$2,$3)", testWorkspaceID, issueID, sessionID)
			if reason == "archived" {
				dbfx.Exec(t, "UPDATE chat_session SET status='archived' WHERE id=$1", sessionID)
			} else {
				dbfx.Exec(t, "UPDATE agent_runtime SET provider='pi' WHERE id=(SELECT runtime_id FROM agent WHERE id=$1)", agentID)
			}
			commentID := dbfx.Comment(t, issueID, "Mention remains deliverable")
			issue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(issueID))
			if err != nil {
				t.Fatal(err)
			}
			task, err := testHandler.TaskService.EnqueueTaskForMention(context.Background(), issue, parseUUID(agentID), parseUUID(commentID), service.OriginNamed)
			if err != nil || task.ChatSessionID.Valid || task.IssueID != issue.ID {
				t.Fatalf("mention lost or misrouted: %+v %v", task, err)
			}
			dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issueID)
		})
	}
}
