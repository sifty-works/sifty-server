package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type cliClaudeAdapter struct {
	program string
	model   string
	timeout time.Duration
	maxCtx  int
}

func newCLIClaude(endpoint, model, _ string, timeoutMS, contextChars int, _ map[string]string) (Model, error) {
	prog := endpoint
	if prog == "" {
		prog = "claude"
	}
	t := time.Duration(timeoutMS) * time.Millisecond
	if t <= 0 {
		t = 5 * time.Minute
	}
	return &cliClaudeAdapter{program: prog, model: model, timeout: t, maxCtx: contextChars}, nil
}

func (a *cliClaudeAdapter) Complete(ctx context.Context, r Request) (Response, error) {
	if a.maxCtx > 0 && len(r.Prompt) > a.maxCtx {
		return Response{}, fmt.Errorf("prompt length %d exceeds context limit %d", len(r.Prompt), a.maxCtx)
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	args := []string{"-p", "--output-format", "json", "--model", a.model,
		"--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands", "--no-session-persistence",
		"--permission-mode", "dontAsk", "--no-chrome"}
	if len(r.Schema) > 0 {
		args = append(args, "--json-schema", string(r.Schema))
	}
	out, err := RunCLI(ctx, a.program, args, r.Prompt, IsolatedEnv(), maxModelCLIOutput)
	if err != nil {
		return Response{}, err
	}
	var res struct {
		IsError          bool            `json:"is_error"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Result           string          `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return Response{}, fmt.Errorf("claude output is not valid JSON: %w", err)
	}
	if res.IsError {
		return Response{}, fmt.Errorf("Not available: check that the Claude command line is signed in")
	}
	if len(res.StructuredOutput) > 0 && json.Valid(res.StructuredOutput) {
		return Response{Text: unwrapFence(string(res.StructuredOutput))}, nil
	}
	return Response{Text: unwrapFence(res.Result)}, nil
}
