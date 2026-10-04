package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type chatSteerFixture struct{ session, agent, runtime, head, queued, message string }

func newChatSteerFixture(t *testing.T, negotiated bool) chatSteerFixture {
	t.Helper()
	runtime := dbfx.Runtime(t, "chat-steer", testutil.Cols{"provider": "codex"})
	agent := dbfx.Agent(t, "chat-steer-"+uuid.NewString(), runtime)
	session := dbfx.Insert(t, "chat_session", testutil.Cols{"workspace_id": testWorkspaceID, "agent_id": agent, "creator_id": testUserID, "title": "steer", "status": "active"})
	head := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": session, "status": "running"})
	queued := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "chat_session_id": session})
	message := dbfx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "task_id": queued, "role": "user", "content": "Please also cover cancellation."})
	dbfx.Cleanup(t, `DELETE FROM chat_task_supplement WHERE chat_session_id=$1`, session)
	if negotiated {
		dbfx.InsertNoID(t, "task_supplement_capability", testutil.Cols{"task_id": head, "workspace_id": testWorkspaceID, "capability": protocol.DaemonCapabilityTaskSupplementV1}, "task_id=$1", head)
	}
	return chatSteerFixture{session, agent, runtime, head, queued, message}
}

func steerRequest(t *testing.T, f chatSteerFixture, requestID string) *testutil.Response {
	t.Helper()
	req := withURLParams(newRequest(http.MethodPost, "/steer", map[string]any{"client_request_id": requestID}), "sessionId", f.session, "taskId", f.queued)
	return testutil.Call(t, testHandler.SteerQueuedChatTask, chatPendingCtxAs(t, req, testUserID))
}

func chatSteerPending(t *testing.T, f chatSteerFixture) PendingChatTaskResponse {
	t.Helper()
	var out PendingChatTaskResponse
	req := withURLParam(newRequest(http.MethodGet, "/pending", nil), "sessionId", f.session)
	testutil.Call(t, testHandler.GetPendingChatTask, chatPendingCtxAs(t, req, testUserID)).Want(200).JSON(&out)
	return out
}

func TestChatSteerDeliveryRebindsMessageWithoutStoppingTurn(t *testing.T) {
	f := newChatSteerFixture(t, true)
	before := chatSteerPending(t, f)
	if !before.Steerable || len(before.QueuedTasks) != 1 {
		t.Fatalf("pending before: %+v", before)
	}
	requestID := uuid.NewString()
	var receipt ChatTaskSupplementResponse
	steerRequest(t, f, requestID).Want(http.StatusAccepted).JSON(&receipt)
	if receipt.ActiveTaskID != f.head || receipt.Status != "pending" {
		t.Fatalf("receipt: %+v", receipt)
	}
	steerRequest(t, f, requestID).Want(200)
	steerRequest(t, f, uuid.NewString()).Want(409)
	pending := chatSteerPending(t, f)
	if pending.QueuedTasks[0].SupplementStatus != "pending" {
		t.Fatalf("pending: %+v", pending)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM chat_task_supplement WHERE chat_session_id=$1`, f.session); n != 1 {
		t.Fatalf("duplicate receipts: %d", n)
	}
	claimed, err := testHandler.Queries.ClaimNextChatTaskSupplement(t.Context(), parseUUID(f.head))
	if err != nil || uuidToString(claimed.ChatMessageID) != f.message || claimed.Content != "Please also cover cancellation." {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(f.head))
	if err != nil {
		t.Fatal(err)
	}
	var updates int
	testHandler.Bus.Subscribe(protocol.EventChatSessionUpdated, func(e events.Event) { updates++ })
	ack := func() {
		req := newRequest(http.MethodPost, "/ack", nil)
		testutil.Call(t, func(w http.ResponseWriter, r *http.Request) {
			testHandler.ackChatTaskSupplement(w, r, task, parseUUID(f.message), ackTaskSupplementRequest{Delivered: true})
		}, req).Want(200)
	}
	ack()
	ack()
	if updates != 2 {
		t.Fatalf("missing updates: %d", updates)
	}
	after := chatSteerPending(t, f)
	if after.TaskID != f.head || after.Status != "running" || len(after.QueuedTasks) != 0 {
		t.Fatalf("running turn changed: %+v", after)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM chat_message WHERE id=$1 AND task_id=$2`, f.message, f.head); n != 1 {
		t.Fatal("supplement message did not move into active turn")
	}
	steerRequest(t, f, requestID).Want(200)
}

func TestChatSteerFailurePreservesQueueOrder(t *testing.T) {
	for _, reason := range []string{"timeout", "provider_rejected"} {
		t.Run(reason, func(t *testing.T) {
			f := newChatSteerFixture(t, true)
			first := dbfx.Task(t, f.agent, testutil.Cols{"runtime_id": f.runtime, "chat_session_id": f.session, "priority": 5})
			dbfx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": f.session, "task_id": first, "role": "user", "content": "earlier"})
			before := chatSteerPending(t, f)
			steerRequest(t, f, uuid.NewString()).Want(202)
			if _, err := testHandler.Queries.ClaimNextChatTaskSupplement(t.Context(), parseUUID(f.head)); err != nil {
				t.Fatal(err)
			}
			task, err := testHandler.Queries.GetAgentTask(t.Context(), parseUUID(f.head))
			if err != nil {
				t.Fatal(err)
			}
			testutil.Call(t, func(w http.ResponseWriter, r *http.Request) {
				testHandler.ackChatTaskSupplement(w, r, task, parseUUID(f.message), ackTaskSupplementRequest{Error: reason})
			}, newRequest(http.MethodPost, "/ack", nil)).Want(200)
			steerRequest(t, f, uuid.NewString()).Want(409)
			after := chatSteerPending(t, f)
			if len(after.QueuedTasks) != 2 || after.QueuedTasks[0].TaskID != before.QueuedTasks[0].TaskID || after.QueuedTasks[1].TaskID != before.QueuedTasks[1].TaskID {
				t.Fatalf("queue reordered: before=%+v after=%+v", before, after)
			}
			for _, q := range after.QueuedTasks {
				if q.TaskID == f.queued && (q.SupplementStatus != "failed" || q.SupplementFailureReason != reason) {
					t.Fatalf("failure not exposed: %+v", q)
				}
			}
		})
	}
}

func TestChatSteerRejectsUnnegotiatedEndedAndMalformed(t *testing.T) {
	f := newChatSteerFixture(t, false)
	if chatSteerPending(t, f).Steerable {
		t.Fatal("old daemon advertised steerable")
	}
	steerRequest(t, f, uuid.NewString()).Want(409)
	steerRequest(t, f, "invalid").Want(400)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed' WHERE id=$1`, f.head)
	steerRequest(t, f, uuid.NewString()).Want(409)
	if n := dbfx.Count(t, `SELECT count(*) FROM chat_message WHERE id=$1 AND task_id=$2`, f.message, f.queued); n != 1 {
		t.Fatal("failed steer lost original input")
	}
}

func TestChatSteerLinkedIssueHeadIsNotSteerable(t *testing.T) {
	f := newChatSteerFixture(t, true)
	issue := dbfx.Issue(t, "linked planning task")
	dbfx.Exec(t, "UPDATE agent_task_queue SET issue_id=$2 WHERE id=$1", f.head, issue)
	if chatSteerPending(t, f).Steerable {
		t.Fatal("linked issue head advertised an unsupported steer action")
	}
	steerRequest(t, f, uuid.NewString()).Want(409)
}

func TestChatSteerReceiptQueriesRespectQueueAndWorkspace(t *testing.T) {
	f := newChatSteerFixture(t, true)
	steerRequest(t, f, uuid.NewString()).Want(202)
	q := testHandler.Queries
	params := db.QueuedChatTaskHasActiveSupplementParams{QueuedTaskID: parseUUID(f.queued), WorkspaceID: parseUUID(testWorkspaceID)}
	active, err := q.QueuedChatTaskHasActiveSupplement(t.Context(), params)
	if err != nil || !active {
		t.Fatalf("own receipt: active=%v err=%v", active, err)
	}
	params.WorkspaceID = parseUUID(uuid.NewString())
	active, err = q.QueuedChatTaskHasActiveSupplement(t.Context(), params)
	if err != nil || active {
		t.Fatalf("foreign receipt: active=%v err=%v", active, err)
	}
	list := db.ListChatTaskSupplementsForSessionParams{ChatSessionID: parseUUID(f.session), WorkspaceID: parseUUID(testWorkspaceID), QueuedTaskIds: []pgtype.UUID{parseUUID(f.queued)}}
	rows, err := q.ListChatTaskSupplementsForSession(t.Context(), list)
	if err != nil || len(rows) != 1 {
		t.Fatalf("current queue: rows=%d err=%v", len(rows), err)
	}
	list.QueuedTaskIds = []pgtype.UUID{parseUUID(uuid.NewString())}
	rows, err = q.ListChatTaskSupplementsForSession(t.Context(), list)
	if err != nil || len(rows) != 0 {
		t.Fatalf("historical receipt leaked into current queue: rows=%d err=%v", len(rows), err)
	}
}

func TestChatSteerRequestCannotCrossSessions(t *testing.T) {
	f := newChatSteerFixture(t, true)
	other := newChatSteerFixture(t, true)
	f.queued = other.queued
	steerRequest(t, f, uuid.NewString()).Want(409)
	// Capability lookup never makes another member's private chat readable.
	otherUser := dbfx.User(t, "other-steer", uuid.NewString()+"@example.test")
	dbfx.Exec(t, `UPDATE chat_session SET creator_id=$2 WHERE id=$1`, f.session, otherUser)
	req := withURLParams(newRequest(http.MethodPost, "/steer", map[string]any{"client_request_id": uuid.NewString()}), "sessionId", f.session, "taskId", f.queued)
	testutil.Call(t, testHandler.SteerQueuedChatTask, chatPendingCtxAs(t, req, testUserID)).Want(403)
}

func TestChatSteerDaemonEndpoints(t *testing.T) {
	f := newChatSteerFixture(t, true)
	attachmentID := dbfx.Insert(t, "attachment", testutil.Cols{"workspace_id": testWorkspaceID, "uploader_type": "member", "uploader_id": testUserID, "filename": "context.txt", "url": "https://example.test/private", "content_type": "text/plain", "size_bytes": 12, "chat_session_id": f.session, "chat_message_id": f.message})
	dbfx.Exec(t, `UPDATE chat_message SET content='' WHERE id=$1`, f.message)

	dbfx.Exec(t, `UPDATE agent_runtime SET daemon_id='chat-steer-daemon' WHERE id=$1`, f.runtime)
	steerRequest(t, f, uuid.NewString()).Want(202)
	claimReq := func(daemon string) *http.Request {
		return withURLParam(newDaemonTokenRequest(http.MethodPost, "/claim", nil, testWorkspaceID, daemon), "taskId", f.head)
	}
	testutil.Call(t, testHandler.ClaimTaskSupplement, claimReq("different-daemon")).Want(403)
	var claimed struct {
		CommentID string `json:"comment_id"`
		Content   string `json:"content"`
	}
	testutil.Call(t, testHandler.ClaimTaskSupplement, claimReq("chat-steer-daemon")).Want(200).JSON(&claimed)
	if claimed.CommentID != f.message || !strings.Contains(claimed.Content, attachmentID) || !strings.Contains(claimed.Content, "multica attachment download") || strings.Contains(claimed.Content, "https://example.test/private") {
		t.Fatalf("claim: %+v", claimed)
	}
	ackReq := withURLParams(newDaemonTokenRequest(http.MethodPost, "/ack", map[string]any{"delivered": true}, testWorkspaceID, "chat-steer-daemon"), "taskId", f.head, "commentId", f.message)
	testutil.Call(t, testHandler.AckTaskSupplement, ackReq).Want(200)
	if len(chatSteerPending(t, f).QueuedTasks) != 0 {
		t.Fatal("delivered input still queued")
	}
}
