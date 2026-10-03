package agent

import "encoding/json"

// ContextUsage describes the most recent native request, never cumulative billing.
type ContextUsage struct {
	Used   int64  `json:"used"`
	Window int64  `json:"window"`
	Model  string `json:"model,omitempty"`
}
type Compaction struct {
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	PreTokens  *int64 `json:"pre_tokens,omitempty"`
	PostTokens *int64 `json:"post_tokens,omitempty"`
}

const MessageCompaction MessageType = "compaction"

type claudeContextTracker struct {
	model  string
	used   *int64
	window int64
}

func (c *claudeContextTracker) observe(msg claudeSDKMessage) *Compaction {
	if msg.ParentToolUseID != "" {
		return nil
	}
	if msg.Type == "system" && msg.Subtype == "init" && msg.Model != "" {
		c.model = msg.Model
	}
	if msg.Type == "assistant" {
		var body claudeMessageContent
		var native struct {
			Usage struct {
				Input *int64 `json:"input_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(msg.Message, &native)
		if json.Unmarshal(msg.Message, &body) == nil && body.Usage != nil && native.Usage.Input != nil && body.Usage.InputTokens >= 0 && body.Usage.CacheReadInputTokens >= 0 && body.Usage.CacheCreationInputTokens >= 0 {
			n := body.Usage.InputTokens + body.Usage.CacheReadInputTokens + body.Usage.CacheCreationInputTokens
			c.model, c.used = body.Model, &n
		}
	}
	if msg.Type == "result" {
		if window := msg.ModelUsage[c.model].ContextWindow; window > 0 {
			c.window = window
		}
		// Manual compaction may have no assistant event; accept only an unambiguous native window.
		if c.model == "" && len(msg.ModelUsage) == 1 {
			for _, u := range msg.ModelUsage {
				if u.ContextWindow > 0 {
					c.window = u.ContextWindow
				}
			}
		}
	}
	if msg.Type == "system" && msg.Subtype == "status" && msg.Status == "compacting" {
		return &Compaction{Status: "started"}
	}
	if msg.Type == "system" && msg.Subtype == "compact_boundary" {
		c.used = msg.CompactMetadata.PostTokens
		return &Compaction{Status: "completed", PreTokens: msg.CompactMetadata.PreTokens, PostTokens: msg.CompactMetadata.PostTokens}
	}
	return nil
}
func (c *claudeContextTracker) usage() *ContextUsage {
	if c.used == nil || *c.used < 0 || c.window <= 0 {
		return nil
	}
	return &ContextUsage{Used: *c.used, Window: c.window, Model: c.model}
}
func codexContextUsage(params map[string]any) *ContextUsage {
	u, _ := params["tokenUsage"].(map[string]any)
	last, _ := u["last"].(map[string]any)
	used, ok := last["totalTokens"].(float64)
	window, wok := u["modelContextWindow"].(float64)
	if !ok || !wok || used < 0 || window <= 0 {
		return nil
	}
	return &ContextUsage{Used: int64(used), Window: int64(window)}
}

func newClaudeContextTracker(opts ExecOptions) claudeContextTracker {
	tracker := claudeContextTracker{model: opts.Model}
	if opts.ResumeSessionID != "" && opts.PriorContextUsage != nil {
		tracker.window = opts.PriorContextUsage.Window
		if tracker.model == "" {
			tracker.model = opts.PriorContextUsage.Model
		}
	}
	return tracker
}
