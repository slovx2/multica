//go:build !windows

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/planning"
)

// Protocol fixtures exercise the same process/pipe boundary as the daemon.
// They never resolve a user-installed agent executable.
func TestPlanningProcessLifecycle(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			dir := t.TempDir()
			executable := filepath.Join(dir, "fixture")
			fixture, err := os.ReadFile("testdata/planning_fixture.py")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(executable, fixture, 0700); err != nil {
				t.Fatal(err)
			}
			backend, err := New(provider, Config{ExecutablePath: executable, Logger: slog.Default(), Env: map[string]string{"PLAN_PROVIDER": provider, "PLAN_ROOT": dir}})
			if err != nil {
				t.Fatal(err)
			}
			resume := ""
			var cards []planning.Card
			for _, step := range []struct {
				prompt string
				mode   bool
				kind   string
			}{{"question", true, "user_question"}, {"plan answer Blue", true, "plan"}, {"revise rejected add tests", true, "plan"}, {"approve only record issue", false, ""}, {"question ignore stop", true, "user_question"}} {
				before := len(cards)
				session, err := backend.Execute(context.Background(), step.prompt, ExecOptions{Cwd: dir, Model: "fixture-model", PlanMode: step.mode, ResumeSessionID: resume, Timeout: 20 * time.Second, PersistCard: func(_ context.Context, card planning.Card) error {
					cards = append(cards, card)
					return os.WriteFile(filepath.Join(dir, "persisted"), []byte(card.Key()), 0600)
				}})
				if err != nil {
					t.Fatal(err)
				}
				for range session.Messages {
				}
				result := <-session.Result
				if result.Status != "completed" || result.Error != "" {
					t.Fatalf("%s: %+v", step.prompt, result)
				}
				if resume != "" && result.SessionID != resume {
					t.Fatalf("session changed: %s -> %s", resume, result.SessionID)
				}
				resume = result.SessionID
				if step.kind != "" && (len(cards) != before+1 || cards[len(cards)-1].Kind != step.kind) {
					t.Fatalf("%s cards=%+v", step.prompt, cards)
				}
				if strings.HasPrefix(step.prompt, "revise") && !strings.Contains(cards[len(cards)-1].Markdown, "tests") {
					t.Fatal("stale revised plan")
				}
			}
			var modes []string
			data, err := os.ReadFile(filepath.Join(dir, "modes"))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &modes); err != nil {
				t.Fatal(err)
			}
			if len(modes) != 5 || modes[0] != "plan" || modes[3] == "plan" || modes[4] != "plan" {
				t.Fatalf("modes=%v", modes)
			}
		})
	}
}
