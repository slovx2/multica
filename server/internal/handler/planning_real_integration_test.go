//go:build agentintegration

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/planning"
)

// Opt-in: native agent processes, real built CLI, real HTTP handlers and a
// disposable local database. Only authentication is supplied by the fixture;
// issue create/update, automatic links, card decisions and enqueue are real.
func TestPlanningRealAgentHTTPIntegration(t *testing.T) {
	if os.Getenv("MULTICA_RUN_REAL_AGENT_SMOKE") != "1" {
		t.Skip("set MULTICA_RUN_REAL_AGENT_SMOKE=1 to use native agent accounts")
	}
	if testHandler == nil {
		t.Fatal("local test database required")
	}
	cliPath := filepath.Join(t.TempDir(), "multica")
	build := exec.Command("go", "build", "-o", cliPath, "../../cmd/multica")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(testUserID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-User-ID", testUserID)
			r.Header.Set("X-Workspace-ID", testWorkspaceID)
			next.ServeHTTP(w, r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, member)))
		})
	})
	router.Post("/api/issues", testHandler.CreateIssue)
	router.Get("/api/issues/{id}", testHandler.GetIssue)
	router.Put("/api/issues/{id}", testHandler.UpdateIssue)
	server := httptest.NewServer(router)
	defer server.Close()
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			executable, err := exec.LookPath(provider)
			if err != nil {
				t.Fatal(err)
			}
			agentID, sessionID, initialTaskID, daemonID := planningFixtureRows(t, provider)
			dir := t.TempDir()
			model := "gpt-6-astra"
			if provider == "claude" {
				model = "claude-opus-5-5[1m]"
			}
			if override := os.Getenv("MULTICA_PLAN_TEST_" + strings.ToUpper(provider) + "_MODEL"); override != "" {
				model = override
			}
			resume := ""
			currentTask := initialTaskID
			var lastCard db.ChatCard
			run := func(prompt string, mode bool) {
				t.Helper()
				backend, err := agent.New(provider, agent.Config{ExecutablePath: executable, Logger: slog.Default(), Env: map[string]string{
					"MULTICA_SERVER_URL": server.URL, "MULTICA_WORKSPACE_ID": testWorkspaceID, "MULTICA_TOKEN": "mat_planning_fixture",
					"MULTICA_AGENT_ID": agentID, "MULTICA_TASK_ID": currentTask, "MULTICA_TASK_CONFIG_ROOT": dir, "MULTICA_DAEMON_PORT": "",
				}})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
				defer cancel()
				s, err := backend.Execute(ctx, prompt, agent.ExecOptions{Cwd: dir, Model: model, PlanMode: mode, ResumeSessionID: resume, Timeout: 4 * time.Minute, PersistCard: func(ctx context.Context, card planning.Card) error {
					request := newDaemonTokenRequest("POST", "/cards", card, testWorkspaceID, daemonID)
					request = request.WithContext(middleware.WithDaemonContext(ctx, testWorkspaceID, daemonID))
					request = withURLParam(request, "taskId", currentTask)
					rec := httptest.NewRecorder()
					testHandler.ReportChatCard(rec, request)

					if rec.Code != 200 {
						return fmt.Errorf("persist card: %d %s", rec.Code, rec.Body.String())
					}
					return json.Unmarshal(rec.Body.Bytes(), &lastCard)
				}})
				if err != nil {
					t.Fatal(err)
				}
				for range s.Messages {
				}
				result := <-s.Result
				if result.Status != "completed" {
					t.Fatalf("native round failed: %+v", result)
				}
				if resume != "" && result.SessionID != resume {
					t.Fatalf("lost conversation: %s -> %s", resume, result.SessionID)
				}
				resume = result.SessionID
				dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed', session_id=$2, completed_at=now() WHERE id=$1", currentTask, resume)
				t.Logf("%s completed; mode=%v; card=%s; session retained", provider, mode, lastCard.Kind)
			}
			decide := func(d planning.Decision) (string, bool) {
				t.Helper()
				d.CardID = uuidToString(lastCard.ID)
				req := withChatTestWorkspaceCtx(t, withURLParam(newRequest("POST", "/messages", map[string]any{"card_decision": d}), "sessionId", sessionID))
				var sent SendChatMessageResponse
				testutil.Call(t, testHandler.SendChatMessage, req).Want(201).JSON(&sent)
				currentTask = sent.TaskID
				dbfx.Exec(t, "UPDATE agent_task_queue SET status='running' WHERE id=$1", currentTask)
				var prompt string
				var mode bool
				dbfx.QueryRow(t, "SELECT content FROM chat_message WHERE task_id=$1", currentTask).Scan(&prompt)
				dbfx.QueryRow(t, "SELECT plan_mode FROM chat_session WHERE id=$1", sessionID).Scan(&mode)
				return prompt, mode
			}
			dbfx.Cleanup(t, "DELETE FROM chat_message WHERE chat_session_id=$1", sessionID)
			dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE chat_session_id=$1", sessionID)
			run("Integration fixture ORCHID-73. Use your native AskUserQuestion/request_user_input tool now to ask one single-choice question: Blue or Red? Do not answer it yourself. Stop after requesting input.", true)
			if lastCard.Kind != "user_question" {
				t.Fatal("no question card")
			}
			var question planning.Card
			if err := json.Unmarshal(lastCard.Payload, &question); err != nil {
				t.Fatal(err)
			}
			q := question.Questions[0]
			prompt, mode := decide(planning.Decision{Action: "answer", Answers: []planning.Answer{{QuestionID: q.ID, SelectedOptionIDs: []string{q.Options[0].ID}}}})
			instruction := fmt.Sprintf("\nProduce a short native plan (Claude: write your plan file, WAIT for successful Write, then ExitPlanMode; Codex: final plan item). Plan to create one issue using the actual local CLI %q issue create --title 'ORCHID-73 %s Blue' --output json, then update that same issue description to 'ORCHID-73 Blue with tests'. This executable is the real Multica CLI built from the code under test and connects only to our disposable test server. Do not run those commands until approved. Do not implement product code.", cliPath, provider)
			run(prompt+instruction, mode)
			if lastCard.Kind != "plan" {
				t.Fatal("no plan card")
			}
			prompt, mode = decide(planning.Decision{Action: "reject", Feedback: "Include the exact description ORCHID-73 Blue with tests. Submit a revised native plan; Claude must wait for successful file write then ExitPlanMode."})
			if !mode {
				t.Fatal("rejection left plan mode")
			}
			run(prompt, mode)
			prompt, mode = decide(planning.Decision{Action: "approve"})
			if mode {
				t.Fatal("approval stayed in plan mode")
			}
			run(prompt+fmt.Sprintf("\nExecute the two real CLI commands using %q now, and use --description 'ORCHID-73 Blue with tests' for update. No other implementation work.", cliPath), mode)
			var issueID, description string
			dbfx.QueryRow(t, "SELECT i.id,i.description FROM issue i JOIN issue_chat_session l ON l.issue_id=i.id WHERE l.chat_session_id=$1 ORDER BY i.created_at DESC LIMIT 1", sessionID).Scan(&issueID, &description)
			dbfx.Cleanup(t, "DELETE FROM issue WHERE id=$1", issueID)
			if !strings.Contains(description, "ORCHID-73 Blue with tests") {
				t.Fatalf("real CLI update missing: %q", description)
			}
		})
	}
}
