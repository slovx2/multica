package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ReportChatContext accepts native telemetry only for the active chat task.
func (h *Handler) ReportChatContext(w http.ResponseWriter, r *http.Request) {
	task, ok := h.requireDaemonTaskAccess(w, r, chi.URLParam(r, "taskId"))
	if !ok {
		return
	}
	if !task.ChatSessionID.Valid || task.IssueID.Valid || task.Status != "running" {
		writeError(w, 409, "context requires a running chat task")
		return
	}
	var state map[string]json.RawMessage
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&state) != nil || state == nil {
		writeError(w, 400, "invalid context state")
		return
	}
	for key, raw := range state {
		switch key {
		case "usage":
			var usage *agent.ContextUsage
			if json.Unmarshal(raw, &usage) != nil || (usage != nil && (usage.Used < 0 || usage.Window <= 0)) {
				writeError(w, 400, "invalid context usage")
				return
			}
			if usage == nil {
				delete(state, "usage")
			}
		case "compaction":
			var compact *agent.Compaction
			if json.Unmarshal(raw, &compact) != nil || (compact != nil && compact.Status != "started" && compact.Status != "completed" && compact.Status != "failed") {
				writeError(w, 400, "invalid compaction")
				return
			}
		case "sync":
		default:
			writeError(w, 400, "unknown context field")
			return
		}
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to begin context update")
		return
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)
	if _, err := q.LockChatSessionForRuntimeBind(r.Context(), task.ChatSessionID); err != nil {
		writeError(w, 409, "chat unavailable")
		return
	}
	current, err := q.GetAgentTask(r.Context(), task.ID)
	if err != nil || current.Status != "running" {
		writeError(w, 409, "task no longer running")
		return
	}
	cs, err := q.GetChatSession(r.Context(), task.ChatSessionID)
	if err != nil {
		writeError(w, 404, "chat not found")
		return
	}
	var previous struct {
		RuntimeID string `json:"runtime_id"`
	}
	_ = json.Unmarshal(cs.ContextState, &previous)
	if previous.RuntimeID != "" && previous.RuntimeID != uuidToString(task.RuntimeID) {
		if _, supplied := state["usage"]; !supplied {
			state["usage"] = json.RawMessage(`null`)
		}
	}
	state["runtime_id"], _ = json.Marshal(uuidToString(task.RuntimeID))
	raw, err := json.Marshal(state)
	if err != nil {
		writeError(w, 400, "invalid context")
		return
	}
	if err := q.UpdateChatContextState(r.Context(), db.UpdateChatContextStateParams{ID: cs.ID, WorkspaceID: cs.WorkspaceID, State: raw}); err != nil {
		writeError(w, 500, "failed to save context")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "failed to save context")
		return
	}
	// Session updates are routed privately using ActorID as the recipient.
	h.publishChat(protocol.EventChatSessionUpdated, uuidToString(cs.WorkspaceID), "member", uuidToString(cs.CreatorID), uuidToString(cs.ID), map[string]any{"chat_session_id": uuidToString(cs.ID), "context_changed": true})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// InitiateChatDirectorySync uses the heartbeat channel so it also works from web clients.
func (h *Handler) InitiateChatDirectorySync(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	cs, ok := h.gatePublicChatSessionForUser(w, r, user, ctxWorkspaceID(r.Context()), chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	if !cs.ProjectID.Valid || cs.Status != "active" {
		writeError(w, 409, "chat has no active project")
		return
	}
	carrier, err := h.Queries.GetAgent(r.Context(), cs.AgentID)
	if err != nil || !carrier.RuntimeID.Valid {
		writeError(w, 409, "chat has no runtime")
		return
	}
	if !h.canInvokeAgent(r.Context(), carrier, "member", user, user, uuidToString(cs.WorkspaceID)) {
		writeError(w, 403, "agent invocation not allowed")
		return
	}
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "chat directory sync", uuidToString(carrier.RuntimeID))
	if !ok {
		return
	}
	if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityChatContextV1) {
		writeError(w, 409, "update daemon to sync chat directories")
		return
	}
	resources, err := h.Queries.ListProjectResources(r.Context(), cs.ProjectID)
	if err != nil {
		writeError(w, 500, "failed to read project resources")
		return
	}
	for _, res := range resources {
		if res.ResourceType != "local_directory" {
			continue
		}
		var ref localDirectoryRef
		if json.Unmarshal(res.ResourceRef, &ref) != nil || ref.DaemonID != rt.DaemonID.String {
			continue
		}
		id, err := h.Queries.CreateChatDirectorySync(r.Context(), db.CreateChatDirectorySyncParams{ID: dbid.NewV7(), ChatSessionID: cs.ID, WorkspaceID: cs.WorkspaceID, RuntimeID: rt.ID, ResourceRef: res.ResourceRef})
		if err != nil {
			writeError(w, 500, "failed to request sync")
			return
		}
		_ = h.Queries.DeleteExpiredChatDirectorySync(r.Context())
		h.requestDaemonPendingWork(uuidToString(rt.ID), "chat_directory_sync")
		writeJSON(w, 202, map[string]string{"id": uuidToString(id)})
		return
	}
	writeError(w, 409, "no local directory for this chat runtime")
}

func (h *Handler) GetChatDirectorySync(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	cs, ok := h.gatePublicChatSessionForUser(w, r, user, ctxWorkspaceID(r.Context()), chi.URLParam(r, "sessionId"))
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "syncId"), "sync_id")
	if !ok {
		return
	}
	result, err := h.Queries.GetChatDirectorySync(r.Context(), db.GetChatDirectorySyncParams{ID: id, ChatSessionID: cs.ID, WorkspaceID: cs.WorkspaceID})
	if err != nil {
		writeError(w, 404, "sync not found")
		return
	}
	if result.Status != "completed" && time.Since(result.CreatedAt.Time) > time.Minute {
		result.Status = "timeout"
	}
	writeJSON(w, 200, struct {
		ID        string          `json:"id"`
		Status    string          `json:"status"`
		Result    json.RawMessage `json:"result"`
		CreatedAt string          `json:"created_at"`
	}{uuidToString(result.ID), result.Status, json.RawMessage(result.Result), timestampToString(result.CreatedAt)})
}

func (h *Handler) ReportChatDirectorySync(w http.ResponseWriter, r *http.Request) {
	rt, ok := h.requireDaemonRuntimeAccess(w, r, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	var body struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body) != nil || len(body.Result) == 0 {
		writeError(w, 400, "invalid sync result")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, body.ID, "id")
	if !ok {
		return
	}
	if _, err := h.Queries.CompleteChatDirectorySync(r.Context(), db.CompleteChatDirectorySyncParams{ID: id, RuntimeID: rt.ID, Result: body.Result}); err != nil {
		writeError(w, 409, "sync no longer running")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
