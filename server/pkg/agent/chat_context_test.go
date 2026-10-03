package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeChatContextFixtures(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		data, err := os.ReadFile("testdata/chat-context-claude.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		var tracker claudeContextTracker
		var events []*Compaction
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var msg claudeSDKMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Fatal(err)
			}
			if e := tracker.observe(msg); e != nil {
				events = append(events, e)
			}
		}
		if got := tracker.usage(); got == nil || got.Used != 1629 || got.Window != 1000000 {
			t.Fatalf("usage %+v", got)
		}
		if len(events) != 2 || events[0].Status != "started" || events[1].Status != "completed" || *events[1].PreTokens != 22657 {
			t.Fatalf("events %+v", events)
		}
		tracker.window = 0
		if tracker.usage() != nil {
			t.Fatal("inferred window")
		}
	})
	t.Run("codex", func(t *testing.T) {
		data, err := os.ReadFile("testdata/chat-context-codex.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		var events []*Compaction
		c := &codexClient{notificationProtocol: "unknown", cfg: Config{Logger: slog.Default()}, onMessage: func(m Message) {
			if m.Compaction != nil {
				events = append(events, m.Compaction)
			}
		}}
		c.setThreadID("fixture-thread")
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			c.handleLine(line)
		}
		if got := c.contextUsage; got == nil || got.Used != 14740 || got.Window != 258400 {
			t.Fatalf("usage %+v", got)
		}
		if len(events) != 2 || events[0].Status != "started" || events[1].Status != "completed" || events[1].PreTokens != nil {
			t.Fatalf("events %+v", events)
		}
		c.handleLine(`{"method":"thread/tokenUsage/updated","params":{"threadId":"other","tokenUsage":{"last":{"totalTokens":1},"modelContextWindow":2}}}`)
		if c.contextUsage.Used != 14740 {
			t.Fatal("subagent replaced context")
		}
		if codexContextUsage(map[string]any{}) != nil {
			t.Fatal("inferred usage")
		}
	})
}

func TestClaudeNativeContextUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       *ContextUsage
	}{
		{"native input and caches", `{"model":"native","usage":{"input_tokens":100,"cache_read_input_tokens":20000,"cache_creation_input_tokens":2557}}`, &ContextUsage{Used: 22657, Window: 1000000}},
		{"missing input", `{"model":"native","usage":{}}`, nil},
		{"unknown model window", `{"model":"other","usage":{"input_tokens":100}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tracker claudeContextTracker
			tracker.observe(claudeSDKMessage{Type: "assistant", Message: json.RawMessage(tc.body)})
			tracker.observe(claudeSDKMessage{Type: "result", ModelUsage: map[string]claudeResultModelUsage{"native": {ContextWindow: 1000000}}})
			got := tracker.usage()
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("usage %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestClaudeCompactNativeCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	path := writeFakeCodexAppServer(t, `read line
case "$line" in *'"text":"/compact"'*) ;; *) exit 9;; esac
echo '{"type":"system","subtype":"status","status":"compacting"}'
echo '{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":22657,"post_tokens":1629}}'
echo '{"type":"result","subtype":"success","session_id":"fixture-session","result":"hidden compact text","modelUsage":{"claude-sonnet-5":{"contextWindow":1000000}}}'
`)
	b := &claudeBackend{cfg: Config{ExecutablePath: path, Logger: slog.Default()}}
	session, err := b.Execute(context.Background(), "not sent", ExecOptions{CompactContext: true, ChatContext: true, ResumeSessionID: "fixture-session", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for msg := range session.Messages {
		if msg.Compaction != nil {
			count++
		}
	}
	result := <-session.Result
	if result.Status != "completed" || result.Output != "" || count != 2 || result.ContextUsage == nil {
		t.Fatalf("result %+v events %d", result, count)
	}
}

func TestCodexCompactNativeCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable")
	}
	fixture, err := os.ReadFile("testdata/chat-context-codex.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	script := `read line
echo '{"jsonrpc":"2.0","id":1,"result":{}}'
read line
read line
case "$line" in *'"method":"thread/resume"'*) ;; *) exit 8;; esac
echo '{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"fixture-thread"}}}'
read line
case "$line" in *'"method":"thread/compact/start"'*) ;; *) exit 9;; esac
echo '{"jsonrpc":"2.0","id":3,"result":{}}'
`
	scanner := bufio.NewScanner(strings.NewReader(string(fixture)))
	for scanner.Scan() {
		script += "echo '" + scanner.Text() + "'\n"
	}
	path := writeFakeCodexAppServer(t, script)
	b := &codexBackend{cfg: Config{ExecutablePath: path, Logger: slog.Default(), CodexVersion: "cached-test-version"}}
	session, err := b.Execute(context.Background(), "not sent", ExecOptions{CompactContext: true, ChatContext: true, ResumeSessionID: "fixture-thread", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for msg := range session.Messages {
		if msg.Compaction != nil {
			count++
		}
	}
	result := <-session.Result
	if result.Status != "completed" || result.Output != "" || count != 2 || result.ContextUsage == nil {
		t.Fatalf("result %+v events %d", result, count)
	}
}
