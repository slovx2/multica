package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ChatTaskSupplementResponse struct {
	TaskID        string `json:"task_id"`
	ActiveTaskID  string `json:"active_task_id"`
	MessageID     string `json:"message_id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason,omitempty"`
}

func chatSupplementResponse(row db.ChatTaskSupplement) ChatTaskSupplementResponse {
	return ChatTaskSupplementResponse{TaskID: uuidToString(row.QueuedTaskID), ActiveTaskID: uuidToString(row.TaskID), MessageID: uuidToString(row.ChatMessageID), Status: row.Status, FailureReason: row.FailureReason.String}
}

// SteerQueuedChatTask binds a queued input to the exact active turn. The agent
// lock serializes selection with ClaimTask; the task lock serializes delivery
// with completion. Failure never changes the queued input's priority.
func (h *Handler) SteerQueuedChatTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	session, ok := h.gatePublicChatSessionForUser(w, r, userID, ctxWorkspaceID(r.Context()), chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	queuedID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task id")
	if !ok {
		return
	}
	var req struct {
		ClientRequestID string `json:"client_request_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	requestID, ok := parseUUIDOrBadRequest(w, req.ClientRequestID, "client_request_id")
	if !ok {
		return
	}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: session.AgentID, WorkspaceID: session.WorkspaceID})
	if err != nil || !h.canInvokeAgent(r.Context(), agent, "member", userID, userID, uuidToString(session.WorkspaceID)) {
		writeErrorCode(w, http.StatusForbidden, "invocation_not_allowed", "you cannot run this agent")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to start steering transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if _, err := qtx.LockChatSessionForDelete(r.Context(), session.ID); err != nil {
		writeError(w, 409, "chat session no longer exists")
		return
	}
	if _, err := qtx.GetAgentForClaimUpdate(r.Context(), session.AgentID); err != nil {
		writeError(w, 500, "failed to lock chat agent")
		return
	}
	// Request lookup is independent of the current head: a retried HTTP response
	// may arrive after successful delivery or after the turn has ended.
	existing, err := qtx.GetChatTaskSupplementByRequest(r.Context(), db.GetChatTaskSupplementByRequestParams{
		ChatSessionID: session.ID, WorkspaceID: session.WorkspaceID, AuthorID: parseUUID(userID), ClientRequestID: requestID,
	})
	if err == nil {
		if existing.QueuedTaskID != queuedID {
			writeError(w, 409, "client_request_id already belongs to another queued task")
			return
		}
		writeJSON(w, 200, chatSupplementResponse(existing))
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "failed to load steering request")
		return
	}
	tasks, err := qtx.ListPendingChatTasksForSession(r.Context(), session.ID)
	if err != nil {
		writeError(w, 500, "failed to load pending tasks")
		return
	}
	if len(tasks) == 0 || tasks[0].Status != "running" {
		writeErrorCode(w, 409, "turn_ended", "there is no running reply to steer; the message remains queued")
		return
	}
	head, err := qtx.GetAgentTask(r.Context(), tasks[0].ID)
	if err != nil {
		writeError(w, 500, "failed to load running task")
		return
	}
	row, err := qtx.CreateChatTaskSupplement(r.Context(), db.CreateChatTaskSupplementParams{
		TaskID: head.ID, QueuedTaskID: queuedID, ChatSessionID: session.ID, WorkspaceID: session.WorkspaceID, AuthorID: parseUUID(userID), ClientRequestID: requestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErrorCode(w, 409, "chat_steer_unavailable", "this queued message cannot be steered into the running reply; it remains queued")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to steer queued message")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "failed to commit steering request")
		return
	}
	h.publishChatSupplementUpdate(r, row)
	h.notifyTaskSupplementAvailable(head)
	writeJSON(w, http.StatusAccepted, chatSupplementResponse(row))
}

func (h *Handler) claimChatTaskSupplement(w http.ResponseWriter, r *http.Request, task db.AgentTaskQueue) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to begin chat guidance claim")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	row, err := qtx.ClaimNextChatTaskSupplement(r.Context(), task.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 200, map[string]any{})
		return
	}
	if err != nil {
		writeError(w, 500, "failed to claim additional chat message")
		return
	}
	cs, err := qtx.GetChatSession(r.Context(), task.ChatSessionID)
	if err != nil {
		writeError(w, 500, "failed to load chat session")
		return
	}
	attachments, err := qtx.ListAttachmentsByChatMessage(r.Context(), db.ListAttachmentsByChatMessageParams{ChatMessageID: row.ChatMessageID, WorkspaceID: cs.WorkspaceID})
	if err != nil {
		writeError(w, 500, "failed to load guidance attachments")
		return
	}
	var content strings.Builder
	content.WriteString(row.Content)
	if len(attachments) > 0 {
		content.WriteString("\n\nAttachments on this message:\n")
		for _, attachment := range attachments {
			fmt.Fprintf(&content, "- id=%s filename=%q content_type=%q\n", uuidToString(attachment.ID), attachment.Filename, attachment.ContentType)
		}
		content.WriteString("Use `multica attachment download <id>` to fetch each file locally before referring to it.\n")
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "failed to commit chat guidance claim")
		return
	}
	// The receipt token is opaque to the daemon. Attachment IDs use the same
	// authenticated CLI download flow as an ordinary chat turn, never stale URLs.
	writeJSON(w, 200, map[string]any{"comment_id": uuidToString(row.ChatMessageID), "author_name": row.AuthorName, "content": content.String()})
}

func (h *Handler) ackChatTaskSupplement(w http.ResponseWriter, r *http.Request, task db.AgentTaskQueue, messageID pgtype.UUID, req ackTaskSupplementRequest) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to start steering acknowledgement")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if _, err := qtx.LockChatSessionForTask(r.Context(), task.ID); err != nil {
		writeError(w, 409, "chat session no longer exists")
		return
	}
	if _, err := qtx.GetAgentForClaimUpdate(r.Context(), task.AgentID); err != nil {
		writeError(w, 500, "failed to lock chat agent")
		return
	}
	var row db.ChatTaskSupplement
	if req.Delivered {
		var delivered db.AckChatTaskSupplementDeliveredRow
		delivered, err = qtx.AckChatTaskSupplementDelivered(r.Context(), db.AckChatTaskSupplementDeliveredParams{TaskID: task.ID, ChatMessageID: messageID})
		row = db.ChatTaskSupplement(delivered)
	} else {
		row, err = qtx.AckChatTaskSupplementFailed(r.Context(), db.AckChatTaskSupplementFailedParams{TaskID: task.ID, ChatMessageID: messageID, FailureReason: pgtype.Text{String: stableTaskSupplementFailureReason(req.Error), Valid: true}})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "additional chat message is no longer deliverable")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to acknowledge additional chat message")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "failed to commit steering acknowledgement")
		return
	}
	h.publishChatSupplementUpdate(r, row)
	writeJSON(w, 200, chatSupplementResponse(row))
}

func (h *Handler) publishChatSupplementUpdate(r *http.Request, row db.ChatTaskSupplement) {
	h.publishChat(protocol.EventChatSessionUpdated, uuidToString(row.WorkspaceID), "member", uuidToString(row.AuthorID), uuidToString(row.ChatSessionID), map[string]any{"chat_session_id": uuidToString(row.ChatSessionID), "supplement_status": row.Status})
}

// Chat payloads are private to the executing runtime. Workspace membership by
// itself is insufficient to read or acknowledge an in-flight user's input.
func (h *Handler) requireChatSupplementRuntime(w http.ResponseWriter, r *http.Request, task db.AgentTaskQueue) bool {
	if !task.RuntimeID.Valid {
		writeError(w, 403, "task has no executing runtime")
		return false
	}
	rt, ok := h.requireDaemonRuntimeAccess(w, r, uuidToString(task.RuntimeID))
	if !ok {
		return false
	}
	if daemonID := middleware.DaemonIDFromContext(r.Context()); daemonID != "" {
		if rt.DaemonID.Valid && rt.DaemonID.String == daemonID {
			return true
		}
	} else if userID := requestUserID(r); userID != "" && rt.OwnerID.Valid && userID == uuidToString(rt.OwnerID) {
		return true
	}
	writeError(w, 403, "only the executing runtime may deliver chat guidance")
	return false
}
