// Package planning defines durable, provider-independent chat interactions.
package planning

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Source struct {
	Provider       string          `json:"provider"`
	ConversationID string          `json:"conversation_id"`
	TurnID         string          `json:"turn_id,omitempty"`
	ItemID         string          `json:"item_id"`
	RequestID      json.RawMessage `json:"request_id,omitempty"`
	IsBlocking     bool            `json:"is_blocking"`
}
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}
type FreeText struct {
	Allowed bool `json:"allowed"`
	Secret  bool `json:"secret"`
}
type Question struct {
	ID            string   `json:"id"`
	NativeID      string   `json:"native_id,omitempty"`
	NativeIndex   int      `json:"native_index"`
	Header        string   `json:"header"`
	Text          string   `json:"text"`
	SelectionMode string   `json:"selection_mode"`
	Options       []Option `json:"options"`
	FreeText      FreeText `json:"free_text"`
	Required      bool     `json:"required"`
}
type Card struct {
	SchemaVersion int        `json:"schema_version"`
	Kind          string     `json:"kind"`
	Title         string     `json:"title"`
	Continuation  string     `json:"continuation"`
	Source        Source     `json:"source"`
	Questions     []Question `json:"questions,omitempty"`
	Markdown      string     `json:"markdown,omitempty"`
}

func (c Card) Key() string {
	return c.Source.Provider + ":" + c.Source.ConversationID + ":" + c.Source.TurnID + ":" + c.Source.ItemID
}
func (c Card) Validate() error {
	if c.SchemaVersion != 1 || c.Continuation != "new_turn" || !Supported(c.Source.Provider) || c.Source.ConversationID == "" || c.Source.ItemID == "" {
		return errors.New("invalid planning card source")
	}
	if c.Kind == "plan" {
		if strings.TrimSpace(c.Markdown) == "" {
			return errors.New("plan body is empty")
		}
		return nil
	}
	if c.Kind != "user_question" || len(c.Questions) == 0 {
		return errors.New("questions are required")
	}
	seen := map[string]bool{}
	for _, q := range c.Questions {
		if q.ID == "" || seen[q.ID] || strings.TrimSpace(q.Text) == "" {
			return errors.New("invalid question")
		}
		seen[q.ID] = true
		if q.SelectionMode != "single" && q.SelectionMode != "multiple" && q.SelectionMode != "none" {
			return errors.New("invalid selection mode")
		}
		ids := map[string]bool{}
		for _, o := range q.Options {
			if o.ID == "" || ids[o.ID] || o.Label == "" {
				return errors.New("invalid option")
			}
			ids[o.ID] = true
		}
	}
	return nil
}
func Supported(provider string) bool { return provider == "claude" || provider == "codex" }

// Questions normalizes only the native question payload, never an answer.
func Questions(source Source, raw json.RawMessage) (Card, error) {
	var input struct {
		Questions []struct {
			ID          string `json:"id"`
			Header      string `json:"header"`
			Question    string `json:"question"`
			MultiSelect bool   `json:"multiSelect"`
			IsOther     bool   `json:"isOther"`
			IsSecret    bool   `json:"isSecret"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return Card{}, err
	}
	c := Card{SchemaVersion: 1, Kind: "user_question", Title: "Questions", Continuation: "new_turn", Source: source}
	for i, native := range input.Questions {
		q := Question{ID: fmt.Sprintf("q%d", i), NativeID: native.ID, NativeIndex: i, Header: native.Header, Text: native.Question, SelectionMode: "single", Options: []Option{}, Required: true, FreeText: FreeText{Allowed: source.Provider == "claude" || native.IsOther || len(native.Options) == 0, Secret: native.IsSecret}}
		if native.MultiSelect {
			q.SelectionMode = "multiple"
		}
		if len(native.Options) == 0 {
			q.SelectionMode = "none"
		}
		for j, o := range native.Options {
			q.Options = append(q.Options, Option{ID: fmt.Sprintf("o%d", j), Label: o.Label, Description: o.Description})
		}
		c.Questions = append(c.Questions, q)
	}
	return c, c.Validate()
}

type Answer struct {
	QuestionID        string   `json:"question_id"`
	Skipped           bool     `json:"skipped"`
	SelectedOptionIDs []string `json:"selected_option_ids"`
	Text              string   `json:"text"`
}
type Decision struct {
	CardID   string   `json:"card_id"`
	Action   string   `json:"action"`
	Feedback string   `json:"feedback"`
	Answers  []Answer `json:"answers"`
}

// Prompt validates a decision against the stored snapshot, then produces the
// next turn's input. User text is data; the host supplies the action instruction.
func (c Card) Prompt(d Decision) (string, string, error) {
	if c.Kind == "plan" {
		switch d.Action {
		case "approve":
			return "approved", "The plan has been approved. Use multica issue create/update to record it as issues. ONLY write issues; do not implement the plan.\n\nApproved plan:\n" + c.Markdown + "\n\nUser feedback:\n" + d.Feedback, nil
		case "reject":
			return "rejected", "The plan was rejected. Stay in plan mode and revise it; do not implement.\n\nPrevious plan:\n" + c.Markdown + "\n\nUser feedback:\n" + d.Feedback, nil
		}
		return "", "", errors.New("invalid plan decision")
	}
	if d.Action == "dismiss" {
		return "dismissed", "用户跳过了这些问题，请按你的判断继续。", nil
	}
	if d.Action != "answer" {
		return "", "", errors.New("invalid question decision")
	}
	answers := map[string]Answer{}
	for _, a := range d.Answers {
		if _, exists := answers[a.QuestionID]; exists {
			return "", "", errors.New("duplicate answer")
		}
		answers[a.QuestionID] = a
	}
	if len(answers) != len(c.Questions) {
		return "", "", errors.New("answer every question")
	}
	var b strings.Builder
	b.WriteString("Answers to your previous questions (continue this same conversation):\n")
	for _, q := range c.Questions {
		a, exists := answers[q.ID]
		if !exists {
			return "", "", errors.New("missing answer")
		}
		if a.Skipped {
			fmt.Fprintf(&b, "\n%s\n（跳过，请自行判断）\n", q.Text)
			continue
		}
		if !q.FreeText.Allowed && a.Text != "" {
			return "", "", errors.New("free text is not allowed")
		}
		if q.SelectionMode != "multiple" && len(a.SelectedOptionIDs) > 1 {
			return "", "", errors.New("select only one option")
		}
		labels := []string{}
		seen := map[string]bool{}
		for _, id := range a.SelectedOptionIDs {
			found := false
			for _, o := range q.Options {
				if o.ID == id && !seen[id] {
					labels = append(labels, o.Label)
					found = true
					seen[id] = true
					break
				}
			}
			if !found {
				return "", "", errors.New("invalid or duplicate option")
			}
		}
		if strings.TrimSpace(a.Text) != "" {
			labels = append(labels, a.Text)
		}
		if q.Required && len(labels) == 0 {
			return "", "", errors.New("answer is required")
		}
		fmt.Fprintf(&b, "\n%s\n%s\n", q.Text, strings.Join(labels, "; "))
	}
	return "answered", b.String(), nil
}
