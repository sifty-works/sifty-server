package formread

import "context"

const (
	KindShortText      = "short_text"
	KindChoice         = "choice"
	KindMultipleChoice = "multiple_choice"
	KindYesNo          = "yes_no"
	KindFile           = "file"
)

type Field struct {
	ID       string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Options  []string `json:"options"`
	Values   []string `json:"values"`
	Source   string   `json:"source"`
	Note     string   `json:"note"`
	Evidence string   `json:"evidence"`
	Revision int64    `json:"revision"`
}

type Form struct {
	URL    string
	System string
	Fields []Field
}

type FormReader interface {
	Read(ctx context.Context, url string) (Form, error)
}
