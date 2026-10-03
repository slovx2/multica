package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/attribution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/planning"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// SendDirectChatAction uses the same session/agent lock order and positional
// queue as direct messages, without creating or adopting user-message rows.
func (s *TaskService) SendDirectChatAction(ctx context.Context, session db.ChatSession, agent db.Agent, user pgtype.UUID, action string) (*DirectChatSendResult, error) {
	if action != "compact" {
		return nil, fmt.Errorf("unsupported chat action")
	}
	overlay := s.buildRuntimeMCPOverlay(ctx, user, agent)
	attr, err := s.applyAttributionFallback(ctx, attribution.DirectHumanRun(user, attribution.EvidenceChat, session.ID), agent)
	if err != nil {
		return nil, err
	}
	source, _, evidenceKind, evidenceRef := attributionCreateParams(attr)
	var out DirectChatSendResult
	err = s.runInTx(ctx, func(qtx *db.Queries) error {
		if _, err := qtx.LockChatSessionForRuntimeBind(ctx, session.ID); err != nil {
			return err
		}
		current, err := qtx.GetChatSession(ctx, session.ID)
		if err != nil {
			return err
		}
		if current.Status != "active" {
			return ErrChatSessionArchived
		}
		carrier, err := qtx.GetAgentForClaimUpdate(ctx, current.AgentID)
		if err != nil {
			return err
		}
		if carrier.ArchivedAt.Valid {
			return ErrChatTaskAgentArchived
		}
		if !carrier.RuntimeID.Valid {
			return ErrChatTaskAgentNoRuntime
		}
		runtime, err := qtx.GetAgentRuntime(ctx, carrier.RuntimeID)
		if err != nil {
			return err
		}
		if !planning.Supported(runtime.Provider) {
			return fmt.Errorf("unsupported chat action")
		}
		out.Queued, err = qtx.HasPendingChatTurnForSession(ctx, current.ID)
		if err != nil {
			return err
		}
		task, err := qtx.CreateChatTask(ctx, db.CreateChatTaskParams{
			ID: dbid.NewV7(), AgentID: current.AgentID, RuntimeID: carrier.RuntimeID,
			Priority: 2, ChatSessionID: current.ID, InitiatorUserID: user,
			OriginatorUserID: attr.UserID, AccountableUserID: attr.AccountableUserID,
			ForceFreshSession: pgtype.Bool{Bool: false, Valid: true},
			RuntimeMcpOverlay: overlay.Overlay, RuntimeConnectedApps: overlay.ConnectedApps,
			OriginatorSource: source, TriggerEvidenceKind: evidenceKind, TriggerEvidenceRefID: evidenceRef,
		})
		if err != nil {
			return err
		}
		if _, err := qtx.SetChatTaskInputOwnerSelf(ctx, task.ID); err != nil {
			return err
		}
		out.Task, err = qtx.SetChatTaskAction(ctx, db.SetChatTaskActionParams{ID: task.ID, Action: action})
		if err != nil {
			return err
		}
		return qtx.TouchChatSession(ctx, current.ID)
	})
	if err != nil {
		return nil, err
	}
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, out.Task)
	s.NotifyTaskEnqueued(ctx, out.Task)
	return &out, nil
}
