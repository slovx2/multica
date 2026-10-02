//go:build !windows

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/multica-ai/multica/server/pkg/planning"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Drives the production daemon runTask, environment preparation, provider
// processes and synchronous card HTTP callback. No installed CLI is resolved.
func TestRunTaskPlanningLifecycle(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			root := t.TempDir()
			t.Setenv("PLAN_ROOT", root)
			t.Setenv("PLAN_PROVIDER", provider)
			fixture, err := os.ReadFile("../../pkg/agent/testdata/planning_fixture.py")
			if err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(root, "fixture")
			writeTestExecutable(t, executable, fixture)
			d.cfg.Agents = map[string]AgentEntry{provider: {Path: executable}}
			d.cfg.AgentTimeout = 30 * time.Second
			d.activeStores = make(map[string]int)
			d.activeStoresCond = sync.NewCond(&d.activeStoresMu)
			d.runtimeIndex = map[string]Runtime{"rt-leader": {ID: "rt-leader", Provider: provider}}
			var mu sync.Mutex
			var cards []planning.Card
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/cards") {
					var card planning.Card
					if err := json.NewDecoder(r.Body).Decode(&card); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					mu.Lock()
					cards = append(cards, card)
					mu.Unlock()
					if err := os.WriteFile(filepath.Join(root, "persisted"), []byte(card.Key()), 0600); err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
				}
				w.Write([]byte(`{}`))
			}))
			defer server.Close()
			d.client = NewClient(server.URL)
			d.cfg.ServerBaseURL = server.URL
			var prior TaskResult
			steps := []struct {
				prompt string
				mode   bool
				kind   string
			}{
				{"question", true, "user_question"}, {"plan answer Blue", true, "plan"},
				{"revise rejected add tests", true, "plan"}, {"approve only record issue", false, ""},
				{"question ignore stop", true, "user_question"},
			}
			for i, step := range steps {
				t.Setenv("PLAN_STEP", step.prompt)
				task := leaderReuseTestTask(fmt.Sprintf("planning-turn-%d", i))
				task.IssueID = ""
				task.IsLeaderTask = false
				task.ChatSessionID = "planning-chat"
				task.ChatMessage = step.prompt
				task.PlanMode = step.mode
				task.PriorSessionID = prior.SessionID
				task.PriorWorkDir = prior.WorkDir
				result, err := d.runTask(context.Background(), task, provider, 0, d.logger)
				if err != nil || result.Status != "completed" {
					t.Fatalf("%s: %+v %v", step.prompt, result, err)
				}
				if i > 0 && (result.SessionID != prior.SessionID || !sameDir(t, result.WorkDir, prior.WorkDir)) {
					t.Fatal("lost planning conversation or workdir")
				}
				if result.SessionID == "" {
					t.Fatal("missing native session")
				}
				mu.Lock()
				if step.kind != "" && (len(cards) == 0 || cards[len(cards)-1].Kind != step.kind) {
					t.Errorf("missing %s card", step.kind)
				}
				mu.Unlock()
				prior = result
			}
			var modes []string
			data, err := os.ReadFile(filepath.Join(root, "modes"))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &modes); err != nil {
				t.Fatal(err)
			}
			if len(modes) != 5 || modes[0] != "plan" || modes[3] == "plan" || modes[4] != "plan" {
				t.Fatalf("wrong modes: %v", modes)
			}
		})
	}
}
