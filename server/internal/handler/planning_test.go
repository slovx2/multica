package handler

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/planning"
)

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

func postPlanningCard(t *testing.T, taskID, daemonID string, card planning.Card) db.ChatCard {
	t.Helper()
	var saved db.ChatCard
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
	var issue IssueResponse
	testutil.Call(t, testHandler.CreateIssue, req).Want(201).JSON(&issue)
	dbfx.Cleanup(t, "DELETE FROM issue WHERE id=$1", issue.ID)
	if n := dbfx.Count(t, "SELECT count(*) FROM issue_chat_session WHERE issue_id=$1 AND chat_session_id=$2", issue.ID, sessionID); n != 1 {
		t.Fatal("missing automatic link")
	}
	req = withURLParam(newRequest("PATCH", "/api/issues/"+issue.ID, map[string]any{"description": "Revised plan"}), "id", issue.ID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
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

	// A queued issue task must also route back when the mention is coalesced.
	dbfx.Exec(t, "UPDATE agent_task_queue SET chat_session_id=NULL WHERE id=$1", uuidToString(mention.ID))
	planner, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatal(err)
	}
	merged := testHandler.mergeCommentIntoPendingTask(context.Background(), row, commentAgentTrigger{Agent: planner, Source: commentTriggerSourceMentionAgent}, parseUUID(commentID), pgtype.Text{})
	if merged != commentMergeSucceeded {
		t.Fatalf("merge: %v", merged)
	}
	var routed string
	dbfx.QueryRow(t, "SELECT chat_session_id FROM agent_task_queue WHERE id=$1", uuidToString(mention.ID)).Scan(&routed)
	if routed != sessionID {
		t.Fatal("coalesced mention lost planner")
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
