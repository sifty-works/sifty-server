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

type Answers map[string]string

type Answerer interface {
	Answer(ctx context.Context, v Vacancy, f formread.Form, have Answers) (Answers, error)
}
