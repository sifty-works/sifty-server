package runner

import "time"

type waitError struct{ t time.Time }

func (w waitError) Error() string { return "try again after " + w.t.UTC().Format(time.RFC3339) }

func (w waitError) RetryAt() time.Time { return w.t }

func WaitUntil(t time.Time) error { return waitError{t: t} }
