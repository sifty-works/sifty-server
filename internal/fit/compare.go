package fit

import "github.com/sifty-works/sifty-server/internal/facts"

type Strictness string

const (
	Must   Strictness = "must"
	Prefer Strictness = "prefer"
	Ignore Strictness = "ignore"
)

type Outcome string

const (
	Match    Outcome = "match"
	Mismatch Outcome = "mismatch"
	Unknown  Outcome = "unknown"
	Skipped  Outcome = "skipped"
)

type Verdict struct {
	Outcome Outcome
	Utility float64
	Source  facts.Source
	Reason  string
	Bucket  string
}

type Comparer interface {
	Name() string
	Compare(v Vacancy, p ProfileView, s Strictness) Verdict
}
