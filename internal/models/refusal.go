package models

import (
	"errors"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/config"
)

const paidBulkDelay = 10 * time.Minute

type refusal struct {
	text  string
	say   string
	final bool
	wait  time.Duration
}

func (r *refusal) Error() string { return r.text }

func (r *refusal) Final() bool { return r.final }

func (r *refusal) RetryAt() time.Time {
	if r.wait == 0 {
		return time.Time{}
	}
	return time.Now().Add(r.wait)
}

func (r *refusal) Sentence() string { return r.words("this work", "text") }

func (r *refusal) words(work, item string) string {
	return strings.NewReplacer("{work}", work, "{item}", item).Replace(r.say)
}

var (
	ErrOffline = &refusal{
		text:  "model calls are not available in offline mode",
		say:   "Model calls are unavailable in offline mode. Open Settings.",
		final: true,
	}
	ErrBulkOnPaid = &refusal{
		text:  "Bulk model work is not allowed on a paid provider. Enable 'Allow paid bulk work' first.",
		say:   "The provider is paid, so this work is refused. Allow paid bulk work in Settings.",
		final: true,
		wait:  paidBulkDelay,
	}
	ErrNoProvider = &refusal{
		text:  "no model provider configured",
		say:   "No model is chosen for {work}. Open Settings.",
		final: true,
	}
	ErrProviderUnavailable = &refusal{
		text: "the provider did not answer",
		say:  "The provider did not answer. Check it in Settings.",
	}
	ErrUnusableAnswers = &refusal{
		text: "the provider kept giving answers that could not be used",
		say:  "The provider kept giving answers that could not be used. Check it in Settings.",
	}
	ErrPromptTooLong = &refusal{
		text:  "prompt is longer than the context of the provider",
		say:   "The {item} is too long for the chosen model. Choose a model with a longer context in Settings.",
		final: true,
	}
)

type told struct {
	error
	sentence string
}

func (t told) Unwrap() error { return t.error }

func (t told) Sentence() string { return t.sentence }

func Tell(err error, work, item string) error {
	var r *refusal
	if !errors.As(err, &r) {
		return err
	}
	noun, ok := config.WorkNouns[work]
	if !ok {
		noun = work
	}
	return told{error: err, sentence: r.words(noun, item)}
}
