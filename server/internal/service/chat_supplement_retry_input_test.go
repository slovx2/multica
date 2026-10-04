package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDeliveredChatGuidanceSurvivesRetryInputOwnership(t *testing.T) {
	pool := sharedTestPool(t)
	workspace, user, agent, _ := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspace, user)
	id := func(value string) pgtype.UUID {
		parsed, err := util.ParseUUID(value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	var runtime string
	fx.QueryRow(t, "SELECT runtime_id::text FROM agent WHERE id=$1", agent).Scan(&runtime)
	session := fx.ChatSession(t, agent)
	root := fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "runtime_id": runtime, "status": "failed"})
	fx.Exec(t, "UPDATE agent_task_queue SET chat_input_task_id=id WHERE id=$1", root)
	original := fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "task_id": root, "role": "user", "content": "Implement the original objective"})
	retry := fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "runtime_id": runtime, "status": "running", "chat_input_task_id": root, "retry_of_task_id": root})
	queued := fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "runtime_id": runtime})
	guidance := fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "task_id": queued, "role": "user", "content": "Include cancellation handling"})
	other := fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "runtime_id": runtime})
	fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "task_id": other, "role": "user", "content": "Unrelated next turn"})
	fx.InsertNoID(t, "task_supplement_capability", testutil.Cols{"task_id": retry, "workspace_id": workspace, "capability": "task-supplement-v1"}, "task_id=$1", retry)
	fx.Cleanup(t, "DELETE FROM chat_task_supplement WHERE chat_session_id=$1", session)
	q := db.New(pool)
	_, err := q.CreateChatTaskSupplement(t.Context(), db.CreateChatTaskSupplementParams{TaskID: id(retry), QueuedTaskID: id(queued), ChatSessionID: id(session), WorkspaceID: id(workspace), AuthorID: id(user), ClientRequestID: id(guidance)})
	if err != nil {
		t.Fatal(err)
	}
	assertInput := func(want ...string) {
		t.Helper()
		messages, err := q.ListChatInputMessages(t.Context(), id(root))
		if err != nil || len(messages) != len(want) {
			t.Fatalf("input = %+v, error = %v; want %d messages", messages, err, len(want))
		}
		for i, message := range messages {
			if message.ID != id(want[i]) {
				t.Fatalf("message %d = %s, want %s", i, util.UUIDToString(message.ID), want[i])
			}
		}
	}
	assertInput(original)
	if _, err := q.ClaimNextChatTaskSupplement(t.Context(), id(retry)); err != nil {
		t.Fatal(err)
	}
	assertInput(original)
	if _, err := q.AckChatTaskSupplementDelivered(t.Context(), db.AckChatTaskSupplementDeliveredParams{TaskID: id(retry), ChatMessageID: id(guidance)}); err != nil {
		t.Fatal(err)
	}
	// A subsequent retry keeps consuming the root's batch even when native
	// session recovery is unavailable. The UI still anchors guidance to retry.
	assertInput(original, guidance)
	var owner string
	fx.QueryRow(t, "SELECT task_id::text FROM chat_message WHERE id=$1", guidance).Scan(&owner)
	if owner != retry {
		t.Fatalf("guidance UI owner = %s, want receiving retry %s", owner, retry)
	}
	otherInput, err := q.ListChatInputMessages(t.Context(), id(other))
	if err != nil || len(otherInput) != 1 || otherInput[0].Content != "Unrelated next turn" {
		t.Fatalf("guidance crossed input batches: %+v, %v", otherInput, err)
	}
}
