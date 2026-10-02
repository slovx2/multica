package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/planning"
)

func TestClaudePlanPermissionsAndRelativeSnapshot(t *testing.T) {
	cwd := t.TempDir()
	plans := filepath.Join(cwd, ".claude", "plans")
	if err := os.MkdirAll(plans, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plans, "p.md"), []byte("Current plan"), 0600); err != nil {
		t.Fatal(err)
	}
	p := newClaudePlanning(cwd, nil)
	body, err := p.snapshot(context.Background(), json.RawMessage(`{"planFilePath":".claude/plans/p.md","plan":"stale"}`))
	if err != nil || body != "Current plan" {
		t.Fatalf("relative plan: %q %v", body, err)
	}
	t.Run("symlink paths", func(t *testing.T) {
		alias := filepath.Join(cwd, "alias")
		if err := os.Symlink(plans, alias); err != nil {
			t.Skip(err)
		}
		body, err := p.snapshot(context.Background(), mustMarshal(t, map[string]string{"planFilePath": filepath.Join(alias, "p.md")}))
		if err != nil || body != "Current plan" {
			t.Fatalf("symlink plan: %q %v", body, err)
		}
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(plans, "escape")); err != nil {
			t.Fatal(err)
		}
		if p.safePath(".claude/plans/escape/../outside.md") {
			t.Fatal("symlink traversal escaped the plan directory")
		}
	})
	for _, tc := range []struct {
		tool, path string
		denied     bool
	}{
		{"Bash", "", true}, {"Write", "app.go", true}, {"Write", ".claude/plans/new.md", false}, {"Read", "app.go", false},
	} {
		var out bytes.Buffer
		msg := claudeSDKMessage{RequestID: "permission", Request: mustMarshal(t, map[string]any{"subtype": "can_use_tool", "tool_name": tc.tool, "input": map[string]string{"file_path": tc.path}})}
		handled := p.handle(context.Background(), msg, "s", ExecOptions{PlanMode: true}, &out, func() {})
		if handled != tc.denied {
			t.Fatalf("%s %s: handled=%v", tc.tool, tc.path, handled)
		}
		if tc.denied && !strings.Contains(out.String(), `"behavior":"deny"`) {
			t.Fatal(out.String())
		}
	}
}

func TestCodexPlanningReasoningEffort(t *testing.T) {
	for _, mode := range []string{"plan", "default"} {
		settings := map[string]any{"model": "fixture"}
		params := map[string]any{"input": []any{}, "collaborationMode": map[string]any{"mode": mode, "settings": settings}}
		applyCodexReasoningEffort(params, "high")
		if params["effort"] != "high" || settings["reasoning_effort"] != "high" {
			t.Fatalf("lost effort: %+v", params)
		}
	}
}

func TestClaudePlanFileSnapshotOverridesStaleInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plans", "final.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Revised\nInclude tests"), 0600); err != nil {
		t.Fatal(err)
	}
	p := newClaudePlanning(t.TempDir(), []string{"CLAUDE_CONFIG_DIR=" + dir})
	p.observe(claudeSDKMessage{Message: mustMarshal(t, map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "write", "name": "Write", "input": map[string]any{"file_path": path}}}})})
	p.observe(claudeSDKMessage{Message: json.RawMessage(`{"content":[{"type":"tool_result","tool_use_id":"write","is_error":false}]}`)})
	body, err := p.snapshot(context.Background(), json.RawMessage(`{"plan":"stale"}`))
	if err != nil || !strings.Contains(body, "Include tests") {
		t.Fatalf("%q %v", body, err)
	}
	if _, err := newClaudePlanning(t.TempDir(), nil).snapshot(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty plan accepted")
	}
}

func TestClaudeQuestionPersistenceBeforeDenyAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p := newClaudePlanning(t.TempDir(), nil)
		var out bytes.Buffer
		calls := 0
		opts := ExecOptions{PersistCard: func(_ context.Context, c planning.Card) error {
			if out.Len() != 0 {
				t.Fatal("denied before persistence")
			}
			calls++
			if fail {
				return errors.New("storage unavailable")
			}
			return nil
		}}
		msg := claudeSDKMessage{RequestID: "req", Request: json.RawMessage(`{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"native","input":{"questions":[{"header":"H","question":"Q","options":[{"label":"A"}]}]}}`)}
		if !p.handle(ctx, msg, "s", opts, &out, cancel) {
			t.Fatal("not handled")
		}
		if fail {
			if ctx.Err() == nil || out.Len() != 0 || p.err == nil {
				t.Fatal("storage failure was acknowledged")
			}
			continue
		}
		if !strings.Contains(out.String(), `"behavior":"deny"`) || !p.waiting {
			t.Fatalf("%s", out.String())
		}
		p.handle(ctx, msg, "s", opts, &out, cancel)
		if calls != 1 {
			t.Fatal("duplicate persisted")
		}
	}
}

func TestCodexPlanOnlyCompletedItemCreatesCard(t *testing.T) {
	c, _, _ := newTestCodexClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cards []planning.Card
	c.planning = &codexPlanning{ctx: ctx, cancel: cancel, plans: map[string]string{}, seen: map[string]bool{}, persist: func(_ context.Context, card planning.Card) error { cards = append(cards, card); return nil }}
	base := map[string]any{"threadId": "s", "turnId": "t", "itemId": "i", "delta": "draft"}
	c.planningItem("item/plan/delta", base)
	if len(cards) != 0 {
		t.Fatal("delta finalized plan")
	}
	base["item"] = map[string]any{"id": "i", "type": "plan", "text": "final"}
	c.planningItem("item/completed", base)
	c.planningItem("item/completed", base)
	if len(cards) != 1 || cards[0].Markdown != "final" || c.turnCompleted {
		t.Fatalf("cards=%+v terminal=%v", cards, c.turnCompleted)
	}
}
