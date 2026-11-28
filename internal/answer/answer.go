package answer

import (
	"context"

	"github.com/sifty-works/sifty-server/internal/formread"
)

type Vacancy struct {
	Title   string
	Company string
	Text    string
}

type Source string

const (
	SourceMyData  Source = "my_data"
	SourceQA      Source = "qa"
	SourceDrafted Source = "drafted"
	SourceUser    Source = "user"
)

type Answer struct {
	Values   []string `json:"values,omitempty"`
	Source   Source   `json:"source,omitempty"`
	Note     string   `json:"note,omitempty"`
	Evidence string   `json:"evidence,omitempty"`
}

type Answers map[string]Answer

type Value struct {
	Text  string
	Match []string
}

type QA struct {
	Question string
	Answer   string
}

type Known struct {
	Values     map[string][]Value
	QA         []QA
	Authorized func(label string) (eligible bool, note string, ok bool)
}

type Data interface {
	Known(ctx context.Context) (Known, error)
}

type Rest interface {
	Complete(ctx context.Context, v Vacancy, f formread.Form, have Answers) (Answers, error)
}

type Answerer interface {
	Answer(ctx context.Context, v Vacancy, f formread.Form, have Answers) (Answers, error)
}
