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

// InitiateProjectDirectorySync uses the heartbeat channel without requiring a chat session.
func (h *Handler) InitiateProjectDirectorySync(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	project, ok := h.directorySyncProject(w, r)
	if !ok {
		return
	}
	var body struct {
		AgentID string `json:"agent_id"`
		Force   bool   `json:"force"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body) != nil {
		writeError(w, 400, "invalid sync request")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, body.AgentID, "agent_id")
	if !ok {
		return
	}
	carrier, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, 404, "agent not found")
		return
	}
	if !carrier.RuntimeID.Valid {
		writeError(w, 409, "agent has no runtime")
		return
	}
	if !h.canInvokeAgent(r.Context(), carrier, "member", user, user, uuidToString(project.WorkspaceID)) {
		writeError(w, 403, "agent invocation not allowed")
		return
	}
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "project directory sync", uuidToString(carrier.RuntimeID))
	if !ok {
		return
	}
	if rt.WorkspaceID != project.WorkspaceID {
		writeError(w, 404, "runtime not found")
		return
	}
	if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityChatContextV1) {
		writeError(w, 409, "update daemon to sync project directories")
		return
	}
	resources, err := h.Queries.ListProjectResources(r.Context(), project.ID)
	if err != nil {
		writeError(w, 500, "failed to read project resources")
		return
	}
	for _, res := range resources {
		if res.ResourceType != "local_directory" {
			continue
		}
		var ref localDirectoryRef
		if json.Unmarshal(res.ResourceRef, &ref) != nil || !rt.DaemonID.Valid || ref.DaemonID == "" || ref.DaemonID != rt.DaemonID.String {
			continue
		}
		tx, err := h.TxStarter.Begin(r.Context())
		if err != nil {
			writeError(w, 500, "failed to begin sync request")
			return
		}
		defer tx.Rollback(r.Context())
		q := h.Queries.WithTx(tx)
		// Serialize creation with project deletion so its cleanup cannot miss us.
		if _, err := q.LockProjectForDelete(r.Context(), db.LockProjectForDeleteParams{ID: project.ID, WorkspaceID: project.WorkspaceID}); err != nil {
			writeError(w, 404, "project not found")
			return
		}
		id, err := q.CreateChatDirectorySync(r.Context(), db.CreateChatDirectorySyncParams{ID: dbid.NewV7(), ProjectID: project.ID, RequesterID: parseUUID(user), WorkspaceID: project.WorkspaceID, RuntimeID: rt.ID, ResourceRef: res.ResourceRef, Force: body.Force})
		if err != nil {
			writeError(w, 500, "failed to request sync")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, 500, "failed to save sync request")
			return
		}
		_ = h.Queries.DeleteExpiredChatDirectorySync(r.Context())
		h.requestDaemonPendingWork(uuidToString(rt.ID), "chat_directory_sync")
		writeJSON(w, 202, map[string]string{"id": uuidToString(id)})
		return
	}
	writeError(w, 409, "no local directory for this agent runtime")
}

func (h *Handler) GetProjectDirectorySync(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	project, ok := h.directorySyncProject(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "syncId"), "sync_id")
	if !ok {
		return
	}
	result, err := h.Queries.GetChatDirectorySync(r.Context(), db.GetChatDirectorySyncParams{ID: id, ProjectID: project.ID, WorkspaceID: project.WorkspaceID, RequesterID: parseUUID(user)})
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

// directorySyncProject enforces both the selected workspace and membership.
func (h *Handler) directorySyncProject(w http.ResponseWriter, r *http.Request) (db.Project, bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project_id")
	if !ok {
		return db.Project{}, false
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return db.Project{}, false
	}
	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(workspaceID), "project not found"); !ok {
		return db.Project{}, false
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	if err != nil {
		writeError(w, 404, "project not found")
		return db.Project{}, false
	}
	return project, true
}
