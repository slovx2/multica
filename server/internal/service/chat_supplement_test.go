package service

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestChatSupplementQueueSettlement(t *testing.T) {
	for _, mode := range []string{"delivered", "provider_failed", "turn_ended", "removed", "edit_blocked", "late_delivery", "session_deleted", "removed_after_claim", "late_ack_after_dispatch"} {
		t.Run(mode, func(t *testing.T) {
			pool := sharedTestPool(t)
			workspace, user, agent, _ := seedAttributionFixture(t, pool)
			fx := testutil.New(pool, workspace, user)
			uuid := func(s string) pgtype.UUID {
				id, err := util.ParseUUID(s)
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			var runtime string
			fx.QueryRow(t, "SELECT runtime_id::text FROM agent WHERE id=$1", agent).Scan(&runtime)
			session := fx.ChatSession(t, agent)
			active := fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "status": "running", "runtime_id": runtime})
			queued := fx.Task(t, agent, testutil.Cols{"chat_session_id": session, "priority": 3, "runtime_id": runtime})
			message := fx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": session, "task_id": queued, "role": "user", "content": "Use the smaller scope"})
			fx.InsertNoID(t, "task_supplement_capability", testutil.Cols{"task_id": active, "workspace_id": workspace, "capability": "task-supplement-v1"}, "task_id=$1", active)
			fx.Cleanup(t, "DELETE FROM chat_task_supplement WHERE chat_session_id=$1", session)
			q := db.New(pool)
			row, err := q.CreateChatTaskSupplement(t.Context(), db.CreateChatTaskSupplementParams{TaskID: uuid(active), QueuedTaskID: uuid(queued), ChatSessionID: uuid(session), WorkspaceID: uuid(workspace), AuthorID: uuid(user), ClientRequestID: uuid(message)})
			if err != nil {
				t.Fatal(err)
			}
			if row.Status != "pending" || row.ChatMessageID != uuid(message) {
				t.Fatalf("unexpected receipt: %+v", row)
			}
			if mode == "edit_blocked" {
				svc := NewTaskService(q, pool, nil, events.New())
				_, err := svc.CancelTaskWithResult(t.Context(), uuid(queued), CancelTaskOptions{QueuedOnly: true, QueueAction: "edit", ExpectedChatSession: uuid(session)})
				if !errors.Is(err, ErrChatTaskSteering) {
					t.Fatalf("edit error = %v", err)
				}
				return
			}
			if mode != "removed" {
				claimed, err := q.ClaimNextChatTaskSupplement(t.Context(), uuid(active))
				if err != nil || claimed.Content != "Use the smaller scope" {
					t.Fatalf("claim = %+v, %v", claimed, err)
				}
			}
			switch mode {
			case "delivered":
				for range 2 {
					delivered, err := q.AckChatTaskSupplementDelivered(t.Context(), db.AckChatTaskSupplementDeliveredParams{TaskID: uuid(active), ChatMessageID: uuid(message)})
					if err != nil || delivered.Status != "delivered" {
						t.Fatalf("ack = %+v, %v", delivered, err)
					}
				}
			case "provider_failed":
				_, err = q.AckChatTaskSupplementFailed(t.Context(), db.AckChatTaskSupplementFailedParams{TaskID: uuid(active), ChatMessageID: uuid(message), FailureReason: pgtype.Text{String: "provider_rejected", Valid: true}})
				if err != nil {
					t.Fatal(err)
				}
			case "turn_ended", "removed", "late_delivery", "session_deleted", "removed_after_claim", "late_ack_after_dispatch":
				target := active
				if mode == "removed" || mode == "removed_after_claim" {
					target = queued
				}
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(t.Context())
				qtx := q.WithTx(tx)
				task, err := qtx.CancelAgentTask(t.Context(), uuid(target))
				if err != nil {
					t.Fatal(err)
				}
				if err := SettleTerminalTaskState(t.Context(), qtx, task); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err := q.ClaimNextChatTaskSupplement(t.Context(), uuid(active)); !errors.Is(err, pgx.ErrNoRows) {
					t.Fatalf("claim after cancellation = %v", err)
				}
			}
			if mode == "late_ack_after_dispatch" {
				fx.Exec(t, "UPDATE agent_task_queue SET status='dispatched', dispatched_at=now() WHERE id=$1", queued)
			}
			if mode == "removed_after_claim" || mode == "late_ack_after_dispatch" {
				if _, err := q.AckChatTaskSupplementDelivered(t.Context(), db.AckChatTaskSupplementDeliveredParams{TaskID: uuid(active), ChatMessageID: uuid(message)}); !errors.Is(err, pgx.ErrNoRows) {
					t.Fatalf("ack after queued task left queue = %v", err)
				}
			}
			if mode == "late_delivery" {
				if _, err := q.AckChatTaskSupplementDelivered(t.Context(), db.AckChatTaskSupplementDeliveredParams{TaskID: uuid(active), ChatMessageID: uuid(message)}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "session_deleted" {
				if err := q.DeleteChatSession(t.Context(), db.DeleteChatSessionParams{ID: uuid(session), WorkspaceID: uuid(workspace)}); err != nil {
					t.Fatal(err)
				}
				if n := fx.Count(t, "SELECT count(*) FROM chat_task_supplement WHERE chat_session_id=$1", session); n != 0 {
					t.Fatalf("orphan receipts: %d", n)
				}
				if n := fx.Count(t, "SELECT count(*) FROM task_supplement_capability WHERE task_id=$1", active); n != 0 {
					t.Fatalf("orphan capabilities: %d", n)
				}
				return
			}
			replay, err := q.GetChatTaskSupplementByRequest(t.Context(), db.GetChatTaskSupplementByRequestParams{ChatSessionID: uuid(session), WorkspaceID: uuid(workspace), AuthorID: uuid(user), ClientRequestID: uuid(message)})
			if err != nil || replay.TaskID != uuid(active) {
				t.Fatalf("request replay after settlement = %+v, %v", replay, err)
			}
			receipt, err := q.GetChatTaskSupplementForQueuedTask(t.Context(), uuid(queued))
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantTask, wantQueue := "failed", queued, "queued"
			if mode == "delivered" || mode == "late_delivery" {
				wantStatus, wantTask, wantQueue = "delivered", active, "cancelled"
			}
			if mode == "removed" || mode == "removed_after_claim" {
				wantQueue = "cancelled"
			}
			if mode == "late_ack_after_dispatch" {
				wantQueue = "dispatched"
			}
			if receipt.Status != wantStatus {
				t.Fatalf("receipt status = %s, want %s", receipt.Status, wantStatus)
			}
			var owner, status string
			var priority int
			fx.QueryRow(t, "SELECT m.task_id::text, q.status, q.priority FROM chat_message m JOIN agent_task_queue q ON q.id=$2 WHERE m.id=$1", message, queued).Scan(&owner, &status, &priority)
			if owner != wantTask || status != wantQueue || priority != 3 {
				t.Fatalf("queue state owner=%s status=%s priority=%d", owner, status, priority)
			}
		})
	}
}
