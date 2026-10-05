package handler

import (
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestChatSteerPositionListAndPage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seq    any
		stored int
		want   int32
	}{
		{"explicit", 7, 3, 7}, {"zero", 0, 3, 0},
		{"legacy", nil, 3, 3}, {"empty", nil, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newChatSteerFixture(t, true)
			original := dbfx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": f.session, "task_id": f.head, "role": "user", "content": "original"})
			dbfx.Exec(t, "UPDATE agent_runtime SET daemon_id='steer-position' WHERE id=$1", f.runtime)
			if tc.stored > 0 {
				dbfx.Insert(t, "task_message", testutil.Cols{"task_id": f.head, "seq": tc.stored, "type": "thinking", "content": "before"})
			}
			// A different task must never affect the legacy maximum.
			dbfx.Insert(t, "task_message", testutil.Cols{"task_id": f.queued, "seq": 99, "type": "text", "content": "other task"})
			steerRequest(t, f, uuid.NewString()).Want(202)
			if _, err := testHandler.Queries.ClaimNextChatTaskSupplement(t.Context(), parseUUID(f.head)); err != nil {
				t.Fatal(err)
			}
			ack := func(seq any) {
				body := map[string]any{"delivered": true}
				if seq != nil {
					body["after_seq"] = seq
				}
				req := withURLParams(newDaemonTokenRequest(http.MethodPost, "/ack", body, testWorkspaceID, "steer-position"), "taskId", f.head, "commentId", f.message)
				testutil.Call(t, testHandler.AckTaskSupplement, req).Want(200)
			}
			ack(tc.seq)
			dbfx.Insert(t, "task_message", testutil.Cols{"task_id": f.head, "seq": 100, "type": "text", "content": "after"})
			ack(100) // A replay must not move the original boundary.
			ack(nil)
			var stored int32
			dbfx.QueryRow(t, "SELECT delivered_after_seq FROM chat_task_supplement WHERE chat_message_id=$1", f.message).Scan(&stored)
			if stored != tc.want {
				t.Fatalf("stored position=%d want=%d", stored, tc.want)
			}
			for _, paged := range []bool{false, true} {
				req := withURLParam(newRequest(http.MethodGet, "/messages?limit=10", nil), "sessionId", f.session)
				req = chatPendingCtxAs(t, req, testUserID)
				var rows []map[string]any
				if paged {
					var page struct {
						Messages []map[string]any `json:"messages"`
					}
					testutil.Call(t, testHandler.ListChatMessagesPage, req).Want(200).JSON(&page)
					rows = page.Messages
				} else {
					testutil.Call(t, testHandler.ListChatMessages, req).Want(200).JSON(&rows)
				}
				if len(rows) != 2 {
					t.Fatalf("page=%v rows=%+v", paged, rows)
				}
				for _, row := range rows {
					value, present := row["steer_after_seq"]
					if !present {
						t.Fatal("missing nullable contract field")
					}
					if row["id"] == original && value != nil {
						t.Fatalf("ordinary message has boundary: %+v", row)
					}
					if row["id"] == f.message && value != float64(tc.want) {
						t.Fatalf("guidance boundary: %+v", row)
					}
				}
			}
		})
	}
}

func TestChatSteerPositionRejectsInvalidSequence(t *testing.T) {
	f := newChatSteerFixture(t, true)
	dbfx.Exec(t, "UPDATE agent_runtime SET daemon_id='steer-position' WHERE id=$1", f.runtime)
	steerRequest(t, f, uuid.NewString()).Want(202)
	if _, err := testHandler.Queries.ClaimNextChatTaskSupplement(t.Context(), parseUUID(f.head)); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []any{-1, 1.5, 2147483648, "3"} {
		req := withURLParams(newDaemonTokenRequest(http.MethodPost, "/ack", map[string]any{"delivered": true, "after_seq": seq}, testWorkspaceID, "steer-position"), "taskId", f.head, "commentId", f.message)
		testutil.Call(t, testHandler.AckTaskSupplement, req).Want(400)
	}
	if n := dbfx.Count(t, "SELECT count(*) FROM chat_task_supplement WHERE chat_message_id=$1 AND status='delivering' AND delivered_after_seq IS NULL", f.message); n != 1 {
		t.Fatal("invalid ack mutated receipt")
	}
}

func TestChatSteerPositionMigrationUpDownUp(t *testing.T) {
	tx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	// Shadow only this table so the migration cannot touch other tests' data.
	if _, err := tx.Exec(t.Context(), "CREATE TEMP TABLE chat_task_supplement (id integer) ON COMMIT DROP; SET LOCAL search_path=pg_temp,public; INSERT INTO chat_task_supplement VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"up", "down", "up"} {
		ddl, err := os.ReadFile("../../migrations/583_chat_supplement_delivery_seq." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), string(ddl)); err != nil {
			t.Fatal(err)
		}
		if direction == "up" {
			var empty bool
			if err := tx.QueryRow(t.Context(), "SELECT delivered_after_seq IS NULL FROM chat_task_supplement").Scan(&empty); err != nil || !empty {
				t.Fatalf("legacy boundary not null: %v %v", empty, err)
			}
		}
	}
}

func TestChatSteerPositionsStayIndependentAndScoped(t *testing.T) {
	f := newChatSteerFixture(t, true)
	q := testHandler.Queries
	task, err := q.GetAgentTask(t.Context(), parseUUID(f.head))
	if err != nil {
		t.Fatal(err)
	}
	for _, seq := range []int32{2, 8} {
		steerRequest(t, f, uuid.NewString()).Want(202)
		if _, err := q.ClaimNextChatTaskSupplement(t.Context(), parseUUID(f.head)); err != nil {
			t.Fatal(err)
		}
		testutil.Call(t, func(w http.ResponseWriter, r *http.Request) {
			testHandler.ackChatTaskSupplement(w, r, task, parseUUID(f.message), ackTaskSupplementRequest{Delivered: true, AfterSeq: &seq})
		}, newRequest(http.MethodPost, "/ack", nil)).Want(200)
		f.queued = dbfx.Task(t, f.agent, testutil.Cols{"runtime_id": f.runtime, "chat_session_id": f.session})
		f.message = dbfx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": f.session, "task_id": f.queued, "role": "user", "content": "next guidance"})
	}
	// Pending guidance has no boundary; a forged boundary on a failed receipt
	// is also excluded by the projection's delivered-only predicate.
	steerRequest(t, f, uuid.NewString()).Want(202)
	dbfx.Exec(t, "UPDATE chat_task_supplement SET status='failed', delivered_after_seq=99 WHERE chat_message_id=$1", f.message)
	req := withURLParam(newRequest(http.MethodGet, "/messages", nil), "sessionId", f.session)
	var rows []ChatMessageResponse
	testutil.Call(t, testHandler.ListChatMessages, chatPendingCtxAs(t, req, testUserID)).Want(200).JSON(&rows)
	found := map[int32]bool{}
	for _, row := range rows {
		if row.SteerAfterSeq != nil {
			found[*row.SteerAfterSeq] = true
		}
	}
	if len(found) != 2 || !found[2] || !found[8] {
		t.Fatalf("independent boundaries=%v", found)
	}

	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, parseUUID(row.ID))
	}
	for _, scope := range []struct{ workspace, session string }{
		{uuid.NewString(), f.session}, {testWorkspaceID, uuid.NewString()},
	} {
		positions, err := q.ListChatMessageSteerPositions(t.Context(), db.ListChatMessageSteerPositionsParams{WorkspaceID: parseUUID(scope.workspace), ChatSessionID: parseUUID(scope.session), MessageIds: ids})
		if err != nil || len(positions) != 0 {
			t.Fatalf("cross-scope positions=%v err=%v", positions, err)
		}
	}
}
