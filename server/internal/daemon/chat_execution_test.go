package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/chatconfig"
)

func TestChatExecutionResolutionReadsCatalogOnce(t *testing.T) {
	catalog := agent.Catalog{Models: []agent.Model{{ID: "gpt-test", Thinking: &agent.ModelThinking{SupportedLevels: []agent.ThinkingLevel{{Value: "low"}, {Value: "high"}}}}}}
	reads := stubModelDiscovery(t, map[string]agent.Catalog{"codex": catalog})
	base := taskModelSelection{Model: "gpt-test", ThinkingLevel: "low"}
	got := resolveTaskModelSelection(context.Background(), "codex", agent.Command{}, base, quietTaskLog(), &chatconfig.Overrides{ThinkingLevel: "high"})
	if reads() != 1 || got.ThinkingLevel != "high" || got.FallbackThinkingLevel != "low" || !got.ChatOverrides {
		t.Fatalf("selection=%+v catalog reads=%d", got, reads())
	}
	if base.ThinkingLevel != "low" {
		t.Fatal("agent defaults changed")
	}
}

func TestChatExecutionNeverRetriesOrdinaryAgentConfiguration(t *testing.T) {
	opts := agent.ExecOptions{ThinkingLevel: "high"}
	calls := 0
	executeWithChatOverrideFallback(&opts, taskModelSelection{}, func(agent.ExecOptions) (agent.Result, int32, error) {
		calls++
		return agent.Result{Status: "failed", Error: "invalid reasoning effort"}, 0, nil
	}, quietTaskLog())
	if calls != 1 || opts.ThinkingLevel != "high" {
		t.Fatal("ordinary task used chat fallback")
	}
}

func TestChatExecutionCatalogFallback(t *testing.T) {
	base := taskModelSelection{Model: "gpt-test", ThinkingLevel: "low", ServiceTier: "default"}
	catalog := agent.Catalog{Models: []agent.Model{{ID: "gpt-test", SupportsExplicitStandardServiceTier: true, ServiceTiers: []agent.ModelServiceTier{{ID: "priority", Name: "Fast"}}, Thinking: &agent.ModelThinking{SupportedLevels: []agent.ThinkingLevel{{Value: "low"}, {Value: "high"}}}}}}
	for _, tc := range []struct {
		name, provider string
		catalog        agent.Catalog
		err            error
		overrides      chatconfig.Overrides
		thinking, tier string
		warn           bool
	}{
		{"supported", "codex", catalog, nil, chatconfig.Overrides{ThinkingLevel: "high", ServiceTier: "priority"}, "high", "priority", false},
		{"inherit", "codex", catalog, nil, chatconfig.Overrides{}, "low", "default", false},
		{"stale choices", "codex", catalog, nil, chatconfig.Overrides{ThinkingLevel: "ultra", ServiceTier: "future"}, "low", "default", true},
		{"discovery error", "codex", agent.Catalog{}, errors.New("offline"), chatconfig.Overrides{ThinkingLevel: "high", ServiceTier: "priority"}, "low", "default", true},
		{"fallback catalog", "codex", agent.Catalog{Models: catalog.Models, Fallback: true}, nil, chatconfig.Overrides{ThinkingLevel: "high", ServiceTier: "priority"}, "low", "default", true},
		{"other runtime", "unsupported", catalog, nil, chatconfig.Overrides{ThinkingLevel: "high", ServiceTier: "priority"}, "low", "default", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			got := applyChatExecutionOverrides(base, tc.overrides, tc.provider, func() (agent.Catalog, error) { return tc.catalog, tc.err }, slog.New(slog.NewTextHandler(&logs, nil)))
			if got.ThinkingLevel != tc.thinking || got.ServiceTier != tc.tier {
				t.Fatalf("got %+v", got)
			}
			if strings.Contains(logs.String(), "WARN") != tc.warn {
				t.Fatalf("warnings: %s", logs.String())
			}
			if base.ThinkingLevel != "low" || base.ServiceTier != "default" {
				t.Fatal("agent configuration mutated")
			}
		})
	}
}

func TestChatExecutionUpstreamFallback(t *testing.T) {
	for _, tc := range []struct {
		name, message, output string
		tools                 int32
		launchError           bool
		retry                 bool
	}{
		{name: "tier rejected", message: "service_tier priority is not supported", retry: true},
		{name: "fast mode rejected", message: "This model does not support fast mode", retry: true},
		{name: "effort rejected", message: "invalid reasoning effort high", retry: true},
		{name: "old CLI", message: "unknown option --effort", launchError: true, retry: true},
		{name: "network", message: "connection timed out"},
		{name: "tool already ran", message: "invalid service_tier", tools: 1},
		{name: "output already emitted", message: "invalid service_tier", output: "partial answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			calls := 0
			options := agent.ExecOptions{ThinkingLevel: "high", ServiceTier: "priority", ResumeSessionID: "chat-native"}
			selection := taskModelSelection{ChatOverrides: true, FallbackThinkingLevel: "low", FallbackServiceTier: "default"}
			result, _, _ := executeWithChatOverrideFallback(&options, selection, func(opts agent.ExecOptions) (agent.Result, int32, error) {
				calls++
				if calls == 1 {
					if tc.launchError {
						return agent.Result{}, 0, errors.New(tc.message)
					}
					return agent.Result{Status: "failed", Error: tc.message, Output: tc.output}, tc.tools, nil
				}
				if opts.ThinkingLevel != "low" || opts.ServiceTier != "default" || opts.ResumeSessionID != "chat-native" {
					t.Fatalf("wrong fallback: %+v", opts)
				}
				return agent.Result{Status: "completed"}, 0, nil
			}, slog.New(slog.NewTextHandler(&logs, nil)))
			want := 1
			if tc.retry {
				want = 2
				if result.Status != "completed" || !strings.Contains(logs.String(), "WARN") {
					t.Fatalf("fallback failed: %+v %s", result, logs.String())
				}
			}
			if calls != want {
				t.Fatalf("calls=%d want=%d", calls, want)
			}
		})
	}
}
