package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type ollamaAdapter struct {
	endpoint string
	model    string
	timeout  time.Duration
	maxCtx   int
	client   *http.Client
}

func newOllama(endpoint, model, _ string, timeoutMS, contextChars int, _ map[string]string) (Model, error) {
	t := time.Duration(timeoutMS) * time.Millisecond
	if t <= 0 {
		t = 10 * time.Minute
	}
	return &ollamaAdapter{
		endpoint: endpoint,
		model:    model,
		timeout:  t,
		maxCtx:   contextChars,
		client:   &http.Client{Timeout: t + 10*time.Minute},
	}, nil
}

type ollamaRequest struct {
	Model     string          `json:"model"`
	Prompt    string          `json:"prompt"`
	Stream    bool            `json:"stream"`
	Format    json.RawMessage `json:"format,omitempty"`
	Think     *bool           `json:"think,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	NumCtx    int             `json:"num_ctx,omitempty"`
}

type ollamaResponse struct {
	Response        string `json:"response"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Done            bool   `json:"done"`
}

func (a *ollamaAdapter) Load(ctx context.Context) error {
	req := ollamaRequest{Model: a.model, Stream: false, KeepAlive: "1h"}
	return a.send(ctx, req, nil)
}

func (a *ollamaAdapter) Unload(ctx context.Context) {
	req := ollamaRequest{Model: a.model, Stream: false, KeepAlive: "0s"}
	a.send(context.WithoutCancel(ctx), req, nil)
}

func (a *ollamaAdapter) Complete(ctx context.Context, r Request) (Response, error) {
	if err := checkPromptLength(r.Prompt, a.maxCtx); err != nil {
		return Response{}, err
	}
	numCtx := len(r.Prompt) + 512
	if a.maxCtx > 0 && numCtx > a.maxCtx {
		numCtx = a.maxCtx
	}
	req := ollamaRequest{
		Model:     a.model,
		Prompt:    r.Prompt,
		Stream:    false,
		KeepAlive: "1h",
		NumCtx:    numCtx,
		Format:    r.Schema,
	}
	if r.Think != "" {
		b := r.Think == "on"
		req.Think = &b
	}
	var resp ollamaResponse
	if err := a.send(ctx, req, &resp); err != nil {
		return Response{}, err
	}
	return Response{
		Text:      resp.Response,
		InTokens:  resp.PromptEvalCount,
		OutTokens: resp.EvalCount,
	}, nil
}

func (a *ollamaAdapter) send(ctx context.Context, body ollamaRequest, out interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("ollama: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+"/api/generate", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("ollama: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
