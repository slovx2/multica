package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/planning"
)

func (h *Handler) validateChatPlanMode(w http.ResponseWriter, r *http.Request, agentID pgtype.UUID, enabled bool) bool {
	if !enabled {
		return true
	}
	a, err := h.Queries.GetAgent(r.Context(), agentID)
	if err != nil {
		writeError(w, 404, "agent not found")
		return false
	}
	runtime, err := h.Queries.GetAgentRuntime(r.Context(), a.RuntimeID)
	if err != nil || !planning.Supported(runtime.Provider) {
		writeError(w, 400, "plan mode is supported only by Claude and Codex")
		return false
	}
	return true
}

// Authentication middleware verifies agent task headers. Explicit links use
// the same private-chat gate as opening that conversation in the UI.
func (h *Handler) planningRequest(w http.ResponseWriter, r *http.Request, userID, workspaceID, explicit string, unlink bool) (*http.Request, bool) {
	if explicit != "" {
		cs, ok := h.gatePublicChatSessionForUser(w, r, userID, workspaceID, explicit)
		if !ok {
			return r, false
		}
		return r.WithContext(service.WithPlanningChat(r.Context(), uuidToString(cs.ID), unlink)), true
	}
	if unlink {
		writeError(w, 400, "planning_chat_session_id is required to unlink")
		return r, false
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if actorType != "agent" || r.Header.Get("X-Task-ID") == "" {
		return r, true
	}
	id, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "task id")
	if !ok {
		return r, false
	}
	task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{ID: id, WorkspaceID: parseUUID(workspaceID)})
	if err != nil || uuidToString(task.AgentID) != actorID {
		writeError(w, 403, "invalid task context")
		return r, false
	}
	if task.ChatSessionID.Valid {
		return r.WithContext(service.WithPlanningChat(r.Context(), uuidToString(task.ChatSessionID), false)), true
	}
	return r, true
}

func (h *Handler) ListIssuePlanningChats(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	sessions, err := h.Queries.ListIssuePlanningChats(r.Context(), db.ListIssuePlanningChatsParams{WorkspaceID: issue.WorkspaceID, IssueID: issue.ID})
	if err != nil {
		writeError(w, 500, "failed to list planning chats")
		return
	}
	result := []ChatSessionResponse{}
	// Chats remain private. Linking an issue never grants transcript access.
	for _, cs := range sessions {
		if uuidToString(cs.CreatorID) == requestUserID(r) {
			a, e := h.Queries.GetAgent(r.Context(), cs.AgentID)
			actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(issue.WorkspaceID))
			if e != nil || !h.canAccessPrivateAgent(r.Context(), a, actorType, actorID, uuidToString(issue.WorkspaceID)) {
				continue
			}
			result = append(result, chatSessionToResponse(cs))
		}
	}
	writeJSON(w, 200, result)
}

func (h *Handler) ListChatPlanningIssues(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	cs, ok := h.gatePublicChatSessionForUser(w, r, userID, ctxWorkspaceID(r.Context()), chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	issues, err := h.Queries.ListChatPlanningIssues(r.Context(), db.ListChatPlanningIssuesParams{WorkspaceID: cs.WorkspaceID, ChatSessionID: cs.ID})
	if err != nil {
		writeError(w, 500, "failed to list planning issues")
		return
	}
	result := make([]map[string]string, 0, len(issues))
	for _, issue := range issues {
		result = append(result, map[string]string{"id": uuidToString(issue.ID), "title": issue.Title})
	}
	writeJSON(w, 200, result)
}

// ReportChatCard is synchronous: a successful response is the durable boundary
// after which the daemon may deny/interrupt the provider's pending request.
func (h *Handler) ReportChatCard(w http.ResponseWriter, r *http.Request) {
	task, ok := h.requireDaemonTaskAccess(w, r, chi.URLParam(r, "taskId"))
	if !ok {
		return
	}
	if !task.ChatSessionID.Valid {
		writeError(w, 400, "cards require a chat task")
		return
	}
	if task.Status != "running" {
		writeError(w, 409, "task is not running")
		return
	}
	var card planning.Card
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&card); err != nil {
		writeError(w, 400, "invalid card")
		return
	}
	if err := card.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, 500, "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	if _, err = q.LockChatSessionForRuntimeBind(ctx, task.ChatSessionID); err != nil {
		writeError(w, 409, "chat unavailable")
		return
	}
	cs, err := q.GetChatSession(ctx, task.ChatSessionID)
	if err != nil || cs.Status != "active" || cs.AgentID != task.AgentID {
		writeError(w, 409, "chat unavailable")
		return
	}
	payload, err := json.Marshal(card)
	if err != nil {
		writeError(w, 400, "invalid card")
		return
	}
	saved, err := q.InsertChatCard(ctx, db.InsertChatCardParams{WorkspaceID: cs.WorkspaceID, ChatSessionID: cs.ID, TaskID: task.ID, SourceKey: card.Key(), Kind: card.Kind, Payload: payload})
	if err != nil {
		writeError(w, 500, "failed to persist card")
		return
	}
	// A replay of an older card must never supersede the newer pending plan.
	if card.Kind == "plan" && saved.Status == "pending" {
		if err = q.SupersedeChatPlans(ctx, db.SupersedeChatPlansParams{WorkspaceID: cs.WorkspaceID, ChatSessionID: cs.ID, ID: saved.ID}); err != nil {
			writeError(w, 500, "failed to supersede plans")
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		writeError(w, 500, "failed to commit card")
		return
	}
	writeJSON(w, 200, saved)
}

func (h *Handler) ListChatCards(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	cs, ok := h.gatePublicChatSessionForUser(w, r, userID, ctxWorkspaceID(r.Context()), chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	cards, err := h.Queries.ListChatCards(r.Context(), db.ListChatCardsParams{WorkspaceID: cs.WorkspaceID, ChatSessionID: cs.ID})
	if err != nil {
		writeError(w, 500, "failed to list cards")
		return
	}
	if cards == nil {
		cards = []db.ChatCard{}
	}
	writeJSON(w, 200, cards)
}
