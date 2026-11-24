package models_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/models"
)

func makeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return p
}

var scriptDir, replyScript string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cli-script-")
	if err != nil {
		os.Exit(1)
	}
	scriptDir = dir
	replyScript = filepath.Join(dir, "reply.sh")
	body := "#!/bin/sh\nd=$(dirname \"$0\")\nenv | cut -d= -f1 | sort > \"$d/env.txt\"\ncat \"$d/reply\"\n"
	if err := os.WriteFile(replyScript, []byte(body), 0755); err != nil {
		os.Exit(1)
	}
	warm := exec.Command(replyScript)
	warm.Env = []string{"PATH=" + os.Getenv("PATH")}
	warm.Run()
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func scriptReplying(t *testing.T, reply string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(scriptDir, "reply"), []byte(reply), 0644); err != nil {
		t.Fatalf("write reply: %v", err)
	}
	return replyScript
}

func cliGate(t *testing.T, pc config.ProviderConfig) *models.Gate {
	t.Helper()
	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"p": pc}
	cfg.Work = config.WorkConfig{"reading": "p"}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	t.Cleanup(func() { gate.Stop(context.Background()) })
	return gate
}

func TestCLIClaudeStructuredOutputReturned(t *testing.T) {
	script := scriptReplying(t, `{"is_error":false,"structured_output":{"score":42},"result":""}`)
	pc := providerConfig("cli-claude", script, "sonnet", "local")
	gate := cliGate(t, pc)
	resp, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "rate this"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != `{"score":42}` {
		t.Errorf("text: got %q, want %q", resp.Text, `{"score":42}`)
	}
}

func TestCLIClaudePlainResultReturned(t *testing.T) {
	script := scriptReplying(t, `{"is_error":false,"result":"hello from claude"}`)
	pc := providerConfig("cli-claude", script, "sonnet", "local")
	gate := cliGate(t, pc)
	resp, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hello from claude" {
		t.Errorf("text: got %q, want %q", resp.Text, "hello from claude")
	}
}

func TestCLIClaudeErrorAnswerReturnsError(t *testing.T) {
	script := scriptReplying(t, `{"is_error":true,"result":"something went wrong"}`)
	pc := providerConfig("cli-claude", script, "sonnet", "local")
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error for is_error response")
	}
	if !strings.Contains(err.Error(), "signed in") {
		t.Errorf("error message: got %q, want to contain 'signed in'", err.Error())
	}
}

func TestCLIClaudeIsolatedEnvironment(t *testing.T) {
	script := scriptReplying(t, `{"is_error":false,"result":"ok"}`)
	envFile := filepath.Join(scriptDir, "env.txt")
	t.Setenv("SIFTY_SHOULD_NOT_PASS", "secret")
	pc := providerConfig("cli-claude", script, "sonnet", "local")
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	for _, k := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if k == "SIFTY_SHOULD_NOT_PASS" {
			t.Error("secret environment variable was passed to the script")
		}
	}
}

func TestCLIClaudePromptOverContextRefused(t *testing.T) {
	pc := providerConfig("cli-claude", "/nonexistent/script", "sonnet", "local")
	pc.Context = 10
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "this prompt is too long"})
	if err == nil {
		t.Fatal("expected error for over-context prompt")
	}
}

func TestCLIRequestFailureLeavesProviderStateAndProviderFailureSetsIt(t *testing.T) {
	cases := []struct {
		name   string
		typ    string
		reply  string
		script string
		holds  bool
	}{
		{"opencode tool call", "cli-opencode", `{"type":"tool_call","part":{"type":"tool","text":""}}` + "\n", "", false},
		{"opencode no text", "cli-opencode", `{"type":"status","part":{"type":"status","text":"done"}}` + "\n", "", true},
		{"claude answer that is not JSON", "cli-claude", "plain words", "", false},
		{"claude output over the limit", "cli-claude", "", "head -c 9000000 /dev/zero | tr '\\0' 'a'\n", false},
		{"opencode output over the limit", "cli-opencode", "", "head -c 9000000 /dev/zero | tr '\\0' 'a'\n", false},
		{"opencode error event", "cli-opencode", `{"type":"error","error":"connection failed"}` + "\n", "", true},
		{"claude not signed in", "cli-claude", `{"is_error":true,"result":"x"}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := tc.script
			if script == "" {
				script = scriptReplying(t, tc.reply)
			} else {
				script = makeScript(t, script)
			}
			gate := cliGate(t, providerConfig(tc.typ, script, "m", "local"))
			if _, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"}); err == nil {
				t.Fatal("expected an error")
			}
			state := gate.Status().Providers["p"].State
			if held := state != "" && state != "ok"; held != tc.holds {
				t.Errorf("provider state = %q, holds = %v, want holds = %v", state, held, tc.holds)
			}
		})
	}
}

const toolCallReply = `{"type":"tool_call","part":{"type":"tool","text":""}}` + "\n"
const textReply = `{"type":"text","part":{"type":"text","text":"fine"}}` + "\n"

func backgroundCall(gate *models.Gate) error {
	_, err := gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "hello"})
	return err
}

func isWait(err error) bool {
	_, ok := err.(models.WaitError)
	return ok
}

func TestThreeRequestFailuresInARowHoldTheProvider(t *testing.T) {
	script := scriptReplying(t, toolCallReply)
	gate := cliGate(t, providerConfig("cli-opencode", script, "m", "local"))
	for i := range 3 {
		if err := backgroundCall(gate); err == nil || isWait(err) {
			t.Fatalf("call %d: expected the request failure, got %v", i+1, err)
		}
	}
	if state := gate.Status().Providers["p"].State; state == "" || state == "ok" {
		t.Fatalf("provider state after three request failures = %q, want held", state)
	}
	scriptReplying(t, textReply)
	if err := backgroundCall(gate); !isWait(err) {
		t.Fatalf("expected WaitError with no request made, got %v", err)
	}
}

func TestSuccessBetweenRequestFailuresResetsTheCount(t *testing.T) {
	script := scriptReplying(t, toolCallReply)
	gate := cliGate(t, providerConfig("cli-opencode", script, "m", "local"))
	backgroundCall(gate)
	backgroundCall(gate)
	scriptReplying(t, textReply)
	if err := backgroundCall(gate); err != nil {
		t.Fatalf("success call failed: %v", err)
	}
	scriptReplying(t, toolCallReply)
	backgroundCall(gate)
	backgroundCall(gate)
	if err := backgroundCall(gate); err == nil || isWait(err) {
		t.Fatalf("the third failure after a success should reach the provider, got %v", err)
	}
}

func TestCLIClaudeTimeoutKillsScript(t *testing.T) {
	script := makeScript(t, `sleep 10
printf '{"is_error":false,"result":"ok"}'`)
	pc := providerConfig("cli-claude", script, "sonnet", "local")
	pc.Timeout = config.Duration(150 * time.Millisecond)
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error from timeout")
	}
}

func TestCLIOpencodeTextEventsJoined(t *testing.T) {
	script := scriptReplying(t, `{"type":"text","part":{"type":"text","text":"hello "}}
{"type":"text","part":{"type":"text","text":"world"}}
`)
	pc := providerConfig("cli-opencode", script, "claude-sonnet", "local")
	gate := cliGate(t, pc)
	resp, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hello world" {
		t.Errorf("text: got %q, want %q", resp.Text, "hello world")
	}
}

func TestCLIOpencodeToolEventRefused(t *testing.T) {
	script := scriptReplying(t, `{"type":"tool_call","part":{"type":"tool","text":""}}
`)
	pc := providerConfig("cli-opencode", script, "claude-sonnet", "local")
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error for tool event")
	}
	if !strings.Contains(err.Error(), "tool call") {
		t.Errorf("error: got %q, want to contain 'tool call'", err.Error())
	}
}

func TestCLIOpencodeErrorEventReturnsError(t *testing.T) {
	script := scriptReplying(t, `{"type":"error","error":"connection failed"}
`)
	pc := providerConfig("cli-opencode", script, "claude-sonnet", "local")
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error for error event")
	}
	if !strings.Contains(err.Error(), "opencode error") {
		t.Errorf("error: got %q, want to contain 'opencode error'", err.Error())
	}
}

func TestCLIOpencodeNoTextReturnsError(t *testing.T) {
	script := scriptReplying(t, `{"type":"status","part":{"type":"status","text":"done"}}
`)
	pc := providerConfig("cli-opencode", script, "claude-sonnet", "local")
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error when no text events")
	}
	if !strings.Contains(err.Error(), "No text from opencode") {
		t.Errorf("error: got %q, want to contain 'No text from opencode'", err.Error())
	}
}

func TestCLIOpencodePromptOverContextRefused(t *testing.T) {
	pc := providerConfig("cli-opencode", "/nonexistent/script", "claude-sonnet", "local")
	pc.Context = 10
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "this prompt is too long"})
	if err == nil {
		t.Fatal("expected error for over-context prompt")
	}
}

func TestCLIOpencodeTimeoutKillsScript(t *testing.T) {
	script := makeScript(t, `sleep 10`)
	pc := providerConfig("cli-opencode", script, "claude-sonnet", "local")
	pc.Timeout = config.Duration(150 * time.Millisecond)
	gate := cliGate(t, pc)
	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error from timeout")
	}
}
