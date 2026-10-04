package service

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestChatInputQueryUsesOwnershipIndexes(t *testing.T) {
	pool := sharedTestPool(t)
	workspace, user, agent, _ := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspace, user)
	var runtime string
	fx.QueryRow(t, "SELECT runtime_id::text FROM agent WHERE id=$1", agent).Scan(&runtime)
	task := fx.Task(t, agent, testutil.Cols{"runtime_id": runtime})
	source, err := os.ReadFile("../../pkg/db/queries/chat.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, sql, ok := strings.Cut(string(source), "-- name: ListChatInputMessages :many\n")
	if !ok {
		t.Fatal("query missing")
	}
	sql, _, _ = strings.Cut(sql, "-- name:")
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), "SET LOCAL enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = tx.QueryRow(t.Context(), "EXPLAIN (FORMAT JSON) "+sql, util.MustParseUUID(task)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan any
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	indexedOwner := false
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			if node["Index Name"] == "idx_chat_message_input_owner" && node["Index Cond"] != nil {
				indexedOwner = true
			}
			if node["Relation Name"] == "chat_message" && node["Node Type"] == "Seq Scan" {
				t.Errorf("chat input scans the message table: %s", raw)
			}
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(plan)
	if !indexedOwner {
		t.Fatalf("input-owner index lacks Index Cond: %s", raw)
	}
}
