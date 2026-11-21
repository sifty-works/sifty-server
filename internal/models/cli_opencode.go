package models

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type cliOpencodeAdapter struct {
	program string
	model   string
	timeout time.Duration
	maxCtx  int
}

func newCLIOpencode(endpoint, model, _ string, timeoutMS, contextChars int, _ map[string]string) (Model, error) {
	prog := endpoint
	if prog == "" {
		prog = "opencode"
	}
	t := time.Duration(timeoutMS) * time.Millisecond
	if t <= 0 {
		t = 5 * time.Minute
	}
	return &cliOpencodeAdapter{program: prog, model: model, timeout: t, maxCtx: contextChars}, nil
}

func (a *cliOpencodeAdapter) Complete(ctx context.Context, r Request) (Response, error) {
	if a.maxCtx > 0 && len(r.Prompt) > a.maxCtx {
		return Response{}, fmt.Errorf("prompt length %d exceeds context limit %d", len(r.Prompt), a.maxCtx)
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	env := append(IsolatedEnv(),
		"OPENCODE_DISABLE_PROJECT_CONFIG=1",
		"OPENCODE_DISABLE_EXTERNAL_SKILLS=1",
		"OPENCODE_DISABLE_CLAUDE_CODE_SKILLS=1",
	)
	out, err := RunCLI(ctx, a.program,
		[]string{"run", "--pure", "--format", "json", "--model", a.model},
		r.Prompt, env, maxModelCLIOutput)
	if err != nil {
		return Response{}, err
	}
	var text strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"part"`
			Error any `json:"error"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Type == "error" {
			return Response{}, fmt.Errorf("opencode error: %v", ev.Error)
		}
		if strings.Contains(ev.Type, "tool") || ev.Part.Type == "tool" {
			return Response{}, fmt.Errorf("provider attempted a tool call")
		}
		if ev.Type == "text" {
			text.WriteString(ev.Part.Text)
		}
	}
	if text.Len() == 0 {
		return Response{}, fmt.Errorf("No text from opencode: check the sign-in and the model")
	}
	return Response{Text: unwrapFence(text.String())}, nil
}
