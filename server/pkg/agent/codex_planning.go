package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/pkg/planning"
)

type codexPlanning struct {
	ctx     context.Context
	cancel  context.CancelFunc
	persist func(context.Context, planning.Card) error
	waiting atomic.Bool // durable question; safe across reader and cleanup
	plans   map[string]string
	seen    map[string]bool
}

func (c *codexClient) planningQuestion(raw map[string]json.RawMessage) {
	p := c.planning
	if p == nil || p.persist == nil {
		c.setTurnError("interactive questions require a Multica chat")
		return
	}
	var params struct {
		ThreadID   string `json:"threadId"`
		TurnID     string `json:"turnId"`
		ItemID     string `json:"itemId"`
		IsBlocking bool   `json:"isBlocking"`
	}
	err := json.Unmarshal(raw["params"], &params)
	if err != nil || params.ThreadID != c.getThreadID() || params.TurnID != c.activeTurnID() {
		c.setTurnError("question does not belong to the active turn")
		p.cancel()
		return
	}
	card, err := planning.Questions(planning.Source{Provider: "codex", ConversationID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID, RequestID: raw["id"], IsBlocking: params.IsBlocking}, raw["params"])
	if err == nil && !p.seen[card.Key()] {
		err = p.persist(p.ctx, card)
		if err == nil {
			p.seen[card.Key()] = true
		}
	}
	if err != nil {
		c.setTurnError("persist question: " + err.Error())
		p.cancel()
		return
	}
	if p.waiting.Load() {
		return
	}
	p.waiting.Store(true)
	if c.onFinalAnswer != nil {
		c.onFinalAnswer("Waiting for your response in the chat card.")
	}
	// Run RPC on a separate goroutine so the stdout reader can consume its
	// acknowledgement and the interrupted terminal. Execute retains process
	// ownership until turn/completed, or a bounded protocol failure.
	go func() {
		ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
		defer cancel()
		_, err := c.request(ctx, "turn/interrupt", map[string]any{"threadId": params.ThreadID, "turnId": params.TurnID})
		if err != nil {
			c.setTurnError("interrupt question turn: " + err.Error())
			p.cancel()
			return
		}
		select {
		case <-p.ctx.Done():
		case <-ctx.Done():
			c.setTurnError("timed out waiting for interrupted question turn")
			p.cancel()
		}
	}()
}

func (c *codexClient) planningItem(method string, params map[string]any) bool {
	p := c.planning
	if p == nil || p.persist == nil {
		return false
	}
	item, _ := params["item"].(map[string]any)
	typ, _ := item["type"].(string)
	if method != "item/plan/delta" && typ != "plan" {
		return false
	}
	thread, _ := params["threadId"].(string)
	turn, _ := params["turnId"].(string)
	id, _ := item["id"].(string)
	if method == "item/plan/delta" {
		id, _ = params["itemId"].(string)
	}
	source := planning.Source{Provider: "codex", ConversationID: thread, TurnID: turn, ItemID: id, IsBlocking: true}
	card := planning.Card{SchemaVersion: 1, Kind: "plan", Title: "Plan", Continuation: "new_turn", Source: source}
	key := card.Key()
	switch method {
	case "item/plan/delta":
		delta, _ := params["delta"].(string)
		if len(p.plans[key])+len(delta) > 1<<20 {
			c.setTurnError("plan exceeds size limit")
			p.cancel()
			return true
		}
		p.plans[key] += delta
	case "item/completed":
		// The completed item is authoritative, even when deltas were missed.
		card.Markdown, _ = item["text"].(string)
		err := card.Validate()
		if strings.TrimSpace(card.Markdown) == "" {
			err = errors.New("Codex completed an empty plan")
		}
		if err == nil && !p.seen[key] {
			err = p.persist(p.ctx, card)
			if err == nil {
				p.seen[key] = true
			}
		}
		if err != nil {
			c.setTurnError("persist plan: " + err.Error())
			p.cancel()
		}
		delete(p.plans, key)
		if err == nil && c.onFinalAnswer != nil {
			c.onFinalAnswer("Please review the plan in the chat card.")
		}
	}
	return true
}

func (c *codexClient) capturePlanningModel(raw json.RawMessage) {
	var response struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &response) == nil && response.Model != "" {
		c.resolvedModel = response.Model
	}
}
