package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/planning"
)

// The stream may announce ExitPlanMode before the preceding Write's result.
// Track successful writes/edits and wait for outstanding results before taking
// a snapshot. Never trust input.plan over the actual completed plan file.
type claudePlanning struct {
	mu      sync.Mutex
	pending map[string]string
	path    string
	waiting bool
	err     error
	seen    map[string]bool
	roots   []string
	cwd     string
}

func newClaudePlanning(cwd string, env []string) *claudePlanning {
	p := &claudePlanning{cwd: cwd, pending: map[string]string{}, seen: map[string]bool{}}
	if config, err := claudeConfigDir(env, cwd); err == nil {
		p.roots = append(p.roots, filepath.Join(config, "plans"))
	}
	p.roots = append(p.roots, filepath.Join(cwd, ".claude", "plans"))
	return p
}
func (p *claudePlanning) absolutePath(path string) string {
	if path != "" && !filepath.IsAbs(path) {
		// Preserve symlink/.. traversal for ResolveSymlinksBestEffort; Join
		// would clean it before we can compare the actual destination.
		return p.cwd + string(filepath.Separator) + path
	}
	return path
}
func (p *claudePlanning) safePath(path string) bool {
	if path == "" {
		return false
	}
	real, err := util.ResolveSymlinksBestEffort(p.absolutePath(path))
	if err != nil {
		return false
	}
	for _, root := range p.roots {
		resolvedRoot, err := util.ResolveSymlinksBestEffort(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(resolvedRoot, real)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
func (p *claudePlanning) observe(msg claudeSDKMessage) {
	var content struct {
		Content []struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Name  string `json:"name"`
			Input struct {
				Path string `json:"file_path"`
			} `json:"input"`
			ToolUseID string `json:"tool_use_id"`
			IsError   bool   `json:"is_error"`
		} `json:"content"`
	}
	if json.Unmarshal(msg.Message, &content) != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range content.Content {
		if b.Type == "tool_use" && (b.Name == "Write" || b.Name == "Edit") && p.safePath(b.Input.Path) {
			p.pending[b.ID] = p.absolutePath(b.Input.Path)
		}
		if b.Type == "tool_result" {
			if path, ok := p.pending[b.ToolUseID]; ok {
				if !b.IsError {
					p.path = path
				}
				delete(p.pending, b.ToolUseID)
			}
		}
	}
}
func (p *claudePlanning) snapshot(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Plan string `json:"plan"`
		Path string `json:"planFilePath"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		p.mu.Lock()
		pending, path := len(p.pending), p.path
		p.mu.Unlock()
		if pending == 0 {
			// An explicit path supports resumed plans that were written in an
			// earlier process, while the current successful write wins.
			if path == "" && p.safePath(in.Path) {
				path = p.absolutePath(in.Path)
			}
			if path != "" {
				real, err := filepath.EvalSymlinks(path)
				if err != nil || !p.safePath(real) {
					return "", errors.New("plan file is unavailable or outside the plans directory")
				}
				f, err := os.Open(real)
				if err != nil {
					return "", err
				}
				defer f.Close()
				body, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
				if err != nil {
					return "", err
				}
				if len(body) > 1<<20 || strings.TrimSpace(string(body)) == "" {
					return "", errors.New("plan file is empty or too large")
				}
				return string(body), nil
			}
			if strings.TrimSpace(in.Plan) != "" {
				return in.Plan, nil
			}
			return "", errors.New("Claude submitted a plan without a completed plan file or body")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", errors.New("timed out waiting for the plan file write")
		case <-tick.C:
		}
	}
}
func denyClaudePlanning(w io.Writer, requestID, message string) error {
	return json.NewEncoder(w).Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": requestID, "response": map[string]any{"behavior": "deny", "message": message}}})
}
func (p *claudePlanning) handle(ctx context.Context, msg claudeSDKMessage, sessionID string, opts ExecOptions, w io.Writer, cancel context.CancelFunc) bool {
	var req claudeControlRequestPayload
	if json.Unmarshal(msg.Request, &req) != nil || req.Subtype != "can_use_tool" {
		return false
	}
	p.mu.Lock()
	waiting := p.waiting
	p.mu.Unlock()
	if waiting {
		_ = denyClaudePlanning(w, msg.RequestID, "The interaction has been saved. Stop this turn; the user's response will arrive in the next message.")
		return true
	}
	if req.ToolName != "AskUserQuestion" && req.ToolName != "ExitPlanMode" {
		if !opts.PlanMode {
			return false
		}
		switch req.ToolName {
		case "Read", "Glob", "Grep", "LS", "WebSearch", "WebFetch":
			return false
		case "Write", "Edit":
			var input struct {
				Path string `json:"file_path"`
			}
			if json.Unmarshal(req.Input, &input) == nil && p.safePath(input.Path) {
				return false
			}
		}
		_ = denyClaudePlanning(w, msg.RequestID, "Plan mode permits reading and writing the plan file only. Stop and propose the work in a plan.")
		return true
	}
	if opts.PersistCard == nil {
		_ = denyClaudePlanning(w, msg.RequestID, "Interactive cards require a Multica chat. Stop and ask in an issue comment.")
		return true
	}
	source := planning.Source{Provider: "claude", ConversationID: sessionID, ItemID: req.ToolUseID, IsBlocking: true}
	if source.ItemID == "" {
		source.ItemID = msg.RequestID
	}
	card, err := planning.Questions(source, req.Input)
	if req.ToolName == "ExitPlanMode" {
		var body string
		body, err = p.snapshot(ctx, req.Input)
		card = planning.Card{SchemaVersion: 1, Kind: "plan", Title: "Plan", Continuation: "new_turn", Source: source, Markdown: body}
	}
	if err == nil {
		err = card.Validate()
	}
	if err == nil {
		p.mu.Lock()
		if !p.seen[card.Key()] {
			err = opts.PersistCard(ctx, card)
			if err == nil {
				p.seen[card.Key()] = true
			}
		}
		if err == nil {
			p.waiting = true
		}
		p.mu.Unlock()
	}
	if err != nil {
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		cancel()
		return true
	}
	if err = denyClaudePlanning(w, msg.RequestID, "Saved in Multica. 回答将在下一条消息给出。Stop now, do not implement, guess answers, or resubmit."); err != nil {
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		cancel()
		return true
	}
	// Denial is not terminal in Claude. Bound a model that ignores the stop
	// instruction; process-tree cleanup remains owned by Execute.
	go func() {
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
			cancel()
		}
	}()
	return true
}
