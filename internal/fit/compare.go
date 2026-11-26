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
)

type Goal struct {
	Kind, Value string
	Strictness  Strictness
}

type Verdict struct {
	Outcome Outcome
	Source  facts.Source
	Reason  string
}

type Comparer interface {
	Name() string
	Compare(have []facts.Fact, goal Goal) Verdict
}
