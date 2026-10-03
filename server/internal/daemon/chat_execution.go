package daemon

import (
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/chatconfig"
)

// Chat choices fail closed even when discovery is unavailable. The agent's
// already-resolved configuration remains the fallback and is never mutated.
func applyChatExecutionOverrides(sel taskModelSelection, overrides chatconfig.Overrides, provider string, load func() (agent.Catalog, error), logger *slog.Logger) taskModelSelection {
	apply := func(field, value string, validate func(func() (agent.Catalog, error), string, string, string) (bool, error), target *string) {
		if value == "" {
			return
		}
		ok, err := validate(load, provider, sel.Model, value)
		if err != nil || !ok {
			logger.Warn("chat execution override unavailable; using agent configuration", "field", field, "value", value, "error", err)
			return
		}
		*target = value
	}
	if overrides.ThinkingLevel != "" && !agent.ThinkingControlSupported(provider) {
		logger.Warn("runtime does not support chat thinking overrides; using agent configuration")
	} else {
		apply("thinking_level", overrides.ThinkingLevel, agent.ValidateThinkingLevelWith, &sel.ThinkingLevel)
	}
	if overrides.ServiceTier != "" && (provider != "codex" || !strings.HasPrefix(sel.Model, "gpt-")) {
		logger.Warn("chat service tier requires a GPT Codex model; using agent configuration")
	} else {
		apply("service_tier", overrides.ServiceTier, agent.ValidateServiceTierWith, &sel.ServiceTier)
	}
	return sel
}

// Retry only a rejected option before any output or tool activity. Retrying a
// partially executed turn could duplicate user-visible or external actions.
func executeWithChatOverrideFallback(opts *agent.ExecOptions, selection taskModelSelection, execute func(agent.ExecOptions) (agent.Result, int32, error), logger *slog.Logger) (agent.Result, int32, error) {
	result, tools, err := execute(*opts)
	changedThinking := opts.ThinkingLevel != selection.FallbackThinkingLevel
	changedTier := opts.ServiceTier != selection.FallbackServiceTier
	if !selection.ChatOverrides || (!changedThinking && !changedTier) {
		return result, tools, err
	}
	message := result.Error
	if err != nil {
		message += " " + err.Error()
	}
	if tools != 0 || result.Output != "" || (err == nil && result.Status != "failed") || !unsupportedChatOption(message, changedThinking, changedTier) {
		return result, tools, err
	}
	logger.Warn("runtime rejected chat execution overrides; retrying with agent configuration", "error", message)
	opts.ThinkingLevel, opts.ServiceTier = selection.FallbackThinkingLevel, selection.FallbackServiceTier
	return execute(*opts)
}

func unsupportedChatOption(message string, thinking, tier bool) bool {
	message = strings.ToLower(message)
	rejected := false
	for _, term := range []string{"unsupported", "not supported", "does not support", "invalid", "unknown", "unrecognized", "unexpected argument", "not available", "not allowed", "not enabled", "not eligible"} {
		rejected = rejected || strings.Contains(message, term)
	}
	if !rejected {
		return false
	}
	if tier && (strings.Contains(message, "service_tier") || strings.Contains(message, "servicetier") || strings.Contains(message, "service tier") || strings.Contains(message, "service-tier") || strings.Contains(message, "fast mode")) {
		return true
	}
	if thinking {
		for _, term := range []string{"reasoning", "thinking", "effort", "--variant"} {
			if strings.Contains(message, term) {
				return true
			}
		}
	}
	return false
}
