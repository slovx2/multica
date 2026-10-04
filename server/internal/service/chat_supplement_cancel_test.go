package service

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDeliveredChatGuidanceSurvivesEmptyCancellation(t *testing.T) {
	for _, mode := range []string{"original", "retry", "deferred_original", "deferred_retry"} {
		t.Run(mode, func(t *testing.T) {
			pool := sharedTestPool(t)
			workspace, user, agent, _ := seedAttributionFixture(t, pool)
			fx := testutil.New(pool, workspace, user)
			id := util.MustParseUUID
			var runtime string
			fx.QueryRow(t, "SELECT runtime_id::text FROM agent WHERE id=$1", agent).Scan(&runtime)
			session := fx.ChatSession(t, agent)
			root := fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "runtime_id": runtime, "status": "running"})
			fx.Exec(t, "UPDATE agent_task_queue SET chat_input_task_id=id WHERE id=$1", root)
			active := root
			if mode == "retry" || mode == "deferred_retry" {
				fx.Exec(t, "UPDATE agent_task_queue SET status='failed' WHERE id=$1", root)
				active = fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "runtime_id": runtime, "status": "running", "chat_input_task_id": root, "retry_of_task_id": root})
			}
			original := fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "task_id": root, "role": "user", "content": "Original input"})
			queued := fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "runtime_id": runtime})
			guidance := fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "task_id": queued, "role": "user", "content": "Delivered guidance"})
			attachment := fx.Insert(t, "attachment", testutil.Cols{"workspace_id": workspace, "uploader_type": "member", "uploader_id": user, "filename": "guide.txt", "url": "https://example.test/guide", "content_type": "text/plain", "size_bytes": 1, "chat_session_id": session, "chat_message_id": guidance})
			fx.InsertNoID(t, "task_supplement_capability", testutil.Cols{"task_id": active, "workspace_id": workspace, "capability": "task-supplement-v1"}, "task_id=$1", active)
			fx.Cleanup(t, "DELETE FROM chat_task_supplement WHERE chat_session_id=$1", session)
			q := db.New(pool)
			_, err := q.CreateChatTaskSupplement(t.Context(), db.CreateChatTaskSupplementParams{TaskID: id(active), QueuedTaskID: id(queued), ChatSessionID: id(session), WorkspaceID: id(workspace), AuthorID: id(user), ClientRequestID: id(guidance)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = q.ClaimNextChatTaskSupplement(t.Context(), id(active)); err != nil {
				t.Fatal(err)
			}
			if _, err = q.AckChatTaskSupplementDelivered(t.Context(), db.AckChatTaskSupplementDeliveredParams{TaskID: id(active), ChatMessageID: id(guidance)}); err != nil {
				t.Fatal(err)
			}
			inputs, err := q.ListChatInputMessages(t.Context(), id(root))
			if err != nil || len(inputs) != 2 {
				t.Fatalf("input batch must include each message once: count=%d err=%v", len(inputs), err)
			}
			svc := NewTaskService(q, pool, nil, events.New())
			if mode == "deferred_original" || mode == "deferred_retry" {
				if _, err = q.CancelAgentTask(t.Context(), id(active)); err != nil {
					t.Fatal(err)
				}
				if _, err = q.MarkChatFinalizeDeferred(t.Context(), id(active)); err != nil {
					t.Fatal(err)
				}
				if !svc.FinalizeDeferredCancelledChat(t.Context(), id(active)) {
					t.Fatal("deferred cancel was not settled")
				}
			} else {
				result, err := svc.CancelTaskWithResult(t.Context(), id(active), CancelTaskOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if result.CancelledChatMessage != nil {
					t.Fatal("delivered input must remain in transcript, not become one arbitrary draft")
				}
			}
			if n := fx.Count(t, "SELECT count(*) FROM chat_message WHERE id IN ($1,$2)", original, guidance); n != 2 {
				t.Fatalf("retained %d of 2 user messages", n)
			}
			if n := fx.Count(t, "SELECT count(*) FROM attachment WHERE id=$1 AND chat_message_id=$2", attachment, guidance); n != 1 {
				t.Fatal("guidance attachment was deleted or detached")
			}
			if n := fx.Count(t, "SELECT count(*) FROM chat_message WHERE task_id=$1 AND role='assistant' AND content='Stopped.'", active); n != 1 {
				t.Fatal("retained input has no stopped reply")
			}
		})
	}
}
