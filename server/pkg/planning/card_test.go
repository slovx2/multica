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

func TestSkippedQuestionAnswers(t *testing.T) {
	card, err := Questions(Source{Provider: "codex", ConversationID: "s", ItemID: "i"}, json.RawMessage(`{"questions":[{"question":"Choose a library","options":[{"label":"A"},{"label":"B"}]},{"question":"Choose tests","options":[{"label":"Unit"},{"label":"Integration"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	decision := Decision{Action: "answer", Answers: []Answer{
		{QuestionID: "q0", Skipped: true, SelectedOptionIDs: []string{"o0"}, Text: "Discarded draft"},
		{QuestionID: "q1", SelectedOptionIDs: []string{"o1"}},
	}}
	status, prompt, err := card.Prompt(decision)
	if err != nil || status != "answered" || !strings.Contains(prompt, "Choose a library\n（跳过，请自行判断）") || !strings.Contains(prompt, "Choose tests\nIntegration") || strings.Contains(prompt, "Discarded draft") {
		t.Fatalf("status=%s prompt=%s error=%v", status, prompt, err)
	}
	for _, answers := range [][]Answer{
		{{QuestionID: "q0", Skipped: true}},
		{{QuestionID: "q0", Skipped: true}, {QuestionID: "q0", Skipped: true}},
		{{QuestionID: "q0", Skipped: true}, {QuestionID: "unknown", Skipped: true}},
		{{QuestionID: "q0", Skipped: true}, {QuestionID: "q1"}},
	} {
		if _, _, err := card.Prompt(Decision{Action: "answer", Answers: answers}); err == nil {
			t.Fatalf("accepted incomplete or invalid answers: %+v", answers)
		}
	}
	var legacy Answer
	if err := json.Unmarshal([]byte(`{"question_id":"q0","selected_option_ids":[],"text":""}`), &legacy); err != nil || legacy.Skipped {
		t.Fatalf("legacy answer must not default to skipped: %+v %v", legacy, err)
	}
}

func TestDismissQuestions(t *testing.T) {
	card := Card{Kind: "user_question", Questions: []Question{{ID: "q0", Text: "Choose", Required: true}}}
	status, prompt, err := card.Prompt(Decision{Action: "dismiss"})
	if err != nil || status != "dismissed" || prompt != "用户跳过了这些问题，请按你的判断继续。" {
		t.Fatalf("status=%s prompt=%s error=%v", status, prompt, err)
	}
	if _, _, err := (Card{Kind: "plan"}).Prompt(Decision{Action: "dismiss"}); err == nil {
		t.Fatal("dismiss must not resolve a plan")
	}
}
