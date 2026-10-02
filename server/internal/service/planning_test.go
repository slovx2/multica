package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestPlanningMentionCompletionUsesIssueIdentifier(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
	fx := testutil.New(pool, workspaceID, userID)
	fx.Exec(t, "UPDATE workspace SET issue_prefix='QORA' WHERE id=$1", workspaceID)
	sessionID := fx.ChatSession(t, agentID)
	runtimeID := fx.Runtime(t, "planning-mention-runtime")
	taskID := fx.Task(t, agentID, testutil.Cols{"issue_id": issueID, "chat_session_id": sessionID, "runtime_id": runtimeID})
	fx.Cleanup(t, "DELETE FROM chat_message WHERE chat_session_id=$1", sessionID)
	ctx := context.Background()
	q := db.New(pool)
	task, err := q.GetAgentTask(ctx, util.MustParseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(protocol.TaskCompletedPayload{Output: "Private planning context"})
	if err != nil {
		t.Fatal(err)
	}
	svc := &TaskService{Queries: q, TxStarter: pool}
	row, err := svc.writeChatCompletionOutcome(ctx, q, task, result)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Mentioned in [QORA-%d](mention://issue/%s) — replied on the issue.", issue.Number, issueID)
	if row == nil || row.Content != want {
		t.Fatalf("planning follow-up = %+v, want %q", row, want)
	}
}
