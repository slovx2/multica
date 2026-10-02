package planning

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeQuestionsAndAnswers(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			card, err := Questions(Source{Provider: provider, ConversationID: "session", ItemID: "call", IsBlocking: true}, json.RawMessage(`{"questions":[{"id":"native","header":"Tests","question":"Which tests?","multiSelect":true,"isOther":true,"isSecret":true,"options":[{"label":"Unit","description":"Fast"},{"label":"Integration","description":"Full path"}]}]}`))
			if err != nil {
				t.Fatal(err)
			}
			q := card.Questions[0]
			if q.NativeID != "native" || q.SelectionMode != "multiple" || !q.FreeText.Secret {
				t.Fatalf("lost native fields: %+v", q)
			}
			d := Decision{Action: "answer", Answers: []Answer{{QuestionID: "q0", SelectedOptionIDs: []string{"o0", "o1"}, Text: "also timeout"}}}
			status, prompt, err := card.Prompt(d)
			if err != nil || status != "answered" || !strings.Contains(prompt, "Unit; Integration; also timeout") {
				t.Fatalf("%s %s %v", status, prompt, err)
			}
			d.Answers[0].SelectedOptionIDs = []string{"not-an-option"}
			if _, _, err := card.Prompt(d); err == nil {
				t.Fatal("accepted unknown option")
			}
		})
	}
}

func TestPlanDecisionNeverImplements(t *testing.T) {
	c := Card{Kind: "plan", Markdown: "# Plan\nChange the code"}
	for _, action := range []string{"approve", "reject"} {
		_, prompt, err := c.Prompt(Decision{Action: action, Feedback: "add tests"})
		if err != nil || !strings.Contains(prompt, "do not implement") || !strings.Contains(prompt, "add tests") {
			t.Fatalf("missing issue-only boundary: %s %v", prompt, err)
		}
	}
	if _, _, err := c.Prompt(Decision{Action: "execute"}); err == nil {
		t.Fatal("accepted execute")
	}
}

func TestMalformedCardsAndAnswers(t *testing.T) {
	source := Source{Provider: "codex", ConversationID: "s", ItemID: "i"}
	if _, err := Questions(source, json.RawMessage(`{"questions":[]}`)); err == nil {
		t.Fatal("empty questions accepted")
	}
	c, err := Questions(source, json.RawMessage(`{"questions":[{"question":"Pick one","options":[{"label":"A"},{"label":"B"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, answers := range [][]Answer{nil, {{QuestionID: "q0", SelectedOptionIDs: []string{"o0", "o1"}}}, {{QuestionID: "q0", Text: "not allowed"}}, {{QuestionID: "unknown", SelectedOptionIDs: []string{"o0"}}}} {
		if _, _, err := c.Prompt(Decision{Action: "answer", Answers: answers}); err == nil {
			t.Fatalf("accepted %+v", answers)
		}
	}
}
