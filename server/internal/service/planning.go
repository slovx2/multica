package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/planning"
)

var ErrChatCardDecision = errors.New("invalid or already resolved chat card")
var ErrPlanModeUnsupported = errors.New("plan mode is supported only by Claude and Codex")

type planningChatKey struct{}
type planningChatChange struct {
	ID     string
	Unlink bool
}

func WithPlanningChat(ctx context.Context, id string, unlink bool) context.Context {
	return context.WithValue(ctx, planningChatKey{}, planningChatChange{ID: id, Unlink: unlink})
}

func HasPlanningChat(ctx context.Context) bool {
	_, ok := ctx.Value(planningChatKey{}).(planningChatChange)
	return ok
}

func LinkPlanningContext(ctx context.Context, q *db.Queries, issue db.Issue) error {
	change, ok := ctx.Value(planningChatKey{}).(planningChatChange)
	if !ok {
		return nil
	}
	id, err := util.ParseUUID(change.ID)
	if err != nil {
		return err
	}
	cs, err := q.GetChatSessionInWorkspace(ctx, db.GetChatSessionInWorkspaceParams{ID: id, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		return err
	}
	if change.Unlink {
		return q.UnlinkPlanningChat(ctx, db.UnlinkPlanningChatParams{WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ChatSessionID: cs.ID})
	}
	return q.LinkPlanningChat(ctx, db.LinkPlanningChatParams{WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ChatSessionID: cs.ID})
}

// Called while holding the session lock, inside the message/task transaction.
func prepareChatDecision(ctx context.Context, q *db.Queries, session db.ChatSession, decision planning.Decision) (db.ChatCard, string, string, error) {
	id, err := util.ParseUUID(decision.CardID)
	if err != nil {
		return db.ChatCard{}, "", "", ErrChatCardDecision
	}
	card, err := q.GetChatCardForUpdate(ctx, db.GetChatCardForUpdateParams{ID: id, WorkspaceID: session.WorkspaceID, ChatSessionID: session.ID})
	if err != nil || card.Status != "pending" {
		return db.ChatCard{}, "", "", ErrChatCardDecision
	}
	var payload planning.Card
	if err := json.Unmarshal(card.Payload, &payload); err != nil {
		return card, "", "", err
	}
	status, prompt, err := payload.Prompt(decision)
	if err != nil {
		return card, "", "", fmt.Errorf("%w: %s", ErrChatCardDecision, err)
	}
	return card, status, prompt, nil
}

// LatestPlanningChat is shared by fresh mention enqueue and queued-comment
// coalescing, so a pending issue run cannot bypass the planner conversation.
func (s *TaskService) LatestPlanningChat(ctx context.Context, issue db.Issue, agentID pgtype.UUID) (pgtype.UUID, error) {
	sessions, err := s.Queries.ListIssuePlanningChats(ctx, db.ListIssuePlanningChatsParams{WorkspaceID: issue.WorkspaceID, IssueID: issue.ID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	for _, cs := range sessions {
		if cs.AgentID == agentID && cs.Status == "active" {
			return cs.ID, nil
		}
	}
	return pgtype.UUID{}, nil
}
