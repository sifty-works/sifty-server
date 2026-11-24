package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Model interface {
	Complete(ctx context.Context, r Request) (Response, error)
}

type Request struct {
	Prompt      string
	Schema      json.RawMessage
	Temperature *float64
	Think       string
}

type Response struct {
	Text      string
	InTokens  int
	OutTokens int
}

var ErrOffline = errors.New("model calls are not available in offline mode")
var ErrBulkOnPaid = errors.New("Bulk model work is not allowed on a paid provider. Enable 'Allow paid bulk work' first.")
var ErrNoProvider = errors.New("no model provider configured")

var ErrPromptTooLong = errors.New("prompt is longer than the context of the provider")

type requestError struct{ err error }

func (e requestError) Error() string { return e.err.Error() }
func (e requestError) Unwrap() error { return e.err }

func requestFailure(err error) error { return requestError{err: err} }

func checkPromptLength(prompt string, limit int) error {
	if limit > 0 && len(prompt) > limit {
		return requestFailure(fmt.Errorf("%w: length %d, limit %d", ErrPromptTooLong, len(prompt), limit))
	}
	return nil
}

func cliRunFailure(err error) error {
	if errors.Is(err, ErrOutputTooLong) {
		return requestFailure(err)
	}
	return err
}

type WaitError struct {
	RetryAfter time.Time
}

func (e WaitError) Error() string {
	return "try again after " + e.RetryAfter.UTC().Format(time.RFC3339)
}

type ProviderState struct {
	State   string       `json:"state"`
	Loading *LoadingInfo `json:"loading,omitempty"`
}

type LoadingInfo struct {
	Model     string `json:"model"`
	StartedAt string `json:"startedAt"`
}

type ModelsStatus struct {
	Providers map[string]ProviderState `json:"providers,omitempty"`
	Paused    bool                     `json:"paused"`
}

type constructor func(endpoint, model, keyEnv string, timeoutMS, contextChars int, secrets map[string]string) (Model, error)

var adapters = map[string]constructor{
	"ollama":       newOllama,
	"openai":       newOpenAI,
	"cli-claude":   newCLIClaude,
	"cli-opencode": newCLIOpencode,
}
