package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/chatconfig"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestChatExecutionSessionScope(t *testing.T) {
	agentID, originalSession, _, _ := planningFixtureRows(t, "codex")
	dbfx.Exec(t, "UPDATE agent SET thinking_level='low', service_tier='default' WHERE id=$1", agentID)
	overrides := chatconfig.Overrides{ThinkingLevel: "high", ServiceTier: "priority"}
	req := withChatTestWorkspaceCtx(t, newRequest("POST", "/chat", map[string]any{"agent_id": agentID, "execution_overrides": overrides}))
	var session ChatSessionResponse
	testutil.Call(t, testHandler.CreateChatSession, req).Want(201).JSON(&session)
	dbfx.Cleanup(t, "DELETE FROM chat_session WHERE id=$1", session.ID)
	if session.ExecutionOverrides != overrides {
		t.Fatalf("create lost overrides: %+v", session)
	}
	req = withChatTestWorkspaceCtx(t, withURLParam(newRequest("PATCH", "/chat", map[string]any{"execution_overrides": map[string]string{"thinking_level": "future-value"}}), "sessionId", session.ID))
	session.ExecutionOverrides = chatconfig.Overrides{}
	testutil.Call(t, testHandler.UpdateChatSession, req).Want(200).JSON(&session)
	if session.ExecutionOverrides.ThinkingLevel != "future-value" || session.ExecutionOverrides.ServiceTier != "" {
		t.Fatalf("replace: %+v", session)
	}
	req = withChatTestWorkspaceCtx(t, withURLParam(newRequest("PATCH", "/chat", map[string]any{"execution_overrides": map[string]string{}}), "sessionId", session.ID))
	session.ExecutionOverrides = chatconfig.Overrides{}
	testutil.Call(t, testHandler.UpdateChatSession, req).Want(200).JSON(&session)
	if !session.ExecutionOverrides.Empty() {
		t.Fatal("clear failed")
	}
	var thinking, tier string
	dbfx.QueryRow(t, "SELECT thinking_level, service_tier FROM agent WHERE id=$1", agentID).Scan(&thinking, &tier)
	if thinking != "low" || tier != "default" {
		t.Fatal("session update changed agent configuration")
	}
	original, err := testHandler.Queries.GetChatSession(context.Background(), parseUUID(originalSession))
	if err != nil || !chatconfig.Decode(original.ExecutionOverrides).Empty() {
		t.Fatalf("other chat changed: %+v %v", original, err)
	}
}

func TestChatExecutionClaimCompatibility(t *testing.T) {
	for _, capable := range []bool{false, true} {
		name := "old daemon"
		if capable {
			name = "new daemon"
		}
		t.Run(name, func(t *testing.T) {
			agentID, sessionID, taskID, daemonID := planningFixtureRows(t, "codex")
			dbfx.Exec(t, "UPDATE agent SET thinking_level='low', service_tier='default' WHERE id=$1", agentID)
			// The user changed settings after enqueue; this run must use its snapshot.
			dbfx.Exec(t, `UPDATE chat_session SET execution_overrides='{"thinking_level":"medium"}' WHERE id=$1`, sessionID)
			dbfx.Exec(t, `UPDATE agent_task_queue SET context='{"execution_overrides":{"thinking_level":"high","service_tier":"priority"}}' WHERE id=$1`, taskID)
			task, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(taskID))
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), task.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			req := newDaemonTokenRequest("POST", "/claim", nil, testWorkspaceID, daemonID)
			if capable {
				req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityChatExecutionOverridesV1)
			}
			resp, _, _, _, _, failure := testHandler.buildClaimedTaskResponse(req, &task, runtime, uuidToString(runtime.ID), testWorkspaceID)
			if failure != nil {
				t.Fatalf("claim: %+v", failure)
			}
			if capable {
				if resp.ExecutionOverrides == nil || resp.ExecutionOverrides.ThinkingLevel != "high" || resp.ExecutionOverrides.ServiceTier != "priority" {
					t.Fatalf("claim lost snapshot: %+v", resp.ExecutionOverrides)
				}
			} else if resp.ExecutionOverrides != nil {
				t.Fatal("old daemon received unsupported overrides")
			}
			if resp.Agent.ThinkingLevel != "low" || resp.Agent.ServiceTier != "default" {
				t.Fatal("claim mutated agent defaults")
			}
			// An ordinary issue task must never inherit chat choices.
			task.ChatSessionID.Valid = false
			task.IssueID = parseUUID(dbfx.Issue(t, "Unrelated issue"))
			resp, _, _, _, _, failure = testHandler.buildClaimedTaskResponse(req, &task, runtime, uuidToString(runtime.ID), testWorkspaceID)
			if failure != nil || resp.ExecutionOverrides != nil {
				t.Fatalf("issue inherited overrides: %+v %+v", resp.ExecutionOverrides, failure)
			}
		})
	}
}

func TestChatExecutionEnqueueSnapshot(t *testing.T) {
	agentID, sessionID, _, _ := planningFixtureRows(t, "codex")
	overrides := chatconfig.Overrides{ThinkingLevel: "high", ServiceTier: "priority"}
	_, err := testHandler.Queries.UpdateChatExecutionOverrides(context.Background(), db.UpdateChatExecutionOverridesParams{ID: parseUUID(sessionID), WorkspaceID: parseUUID(testWorkspaceID), ExecutionOverrides: overrides.JSON()})
	if err != nil {
		t.Fatal(err)
	}
	var runtimeID string
	dbfx.QueryRow(t, "SELECT runtime_id FROM agent WHERE id=$1", agentID).Scan(&runtimeID)
	// Exercise the query used by chat sends, not a hand-built context fixture.
	task, err := testHandler.Queries.CreateChatTask(context.Background(), db.CreateChatTaskParams{AgentID: parseUUID(agentID), RuntimeID: parseUUID(runtimeID), ChatSessionID: parseUUID(sessionID)})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", uuidToString(task.ID))
	var snapshot struct {
		ExecutionOverrides chatconfig.Overrides `json:"execution_overrides"`
	}
	if err := json.Unmarshal(task.Context, &snapshot); err != nil || snapshot.ExecutionOverrides != overrides {
		t.Fatalf("snapshot: %s %v", task.Context, err)
	}
}
