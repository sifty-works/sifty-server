package formread

import "context"

type Field struct {
	ID       string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Options  []string `json:"options"`
}

type Form struct {
	URL    string
	System string
	Fields []Field
}

type FormReader interface {
	Read(ctx context.Context, url string) (Form, error)
}
