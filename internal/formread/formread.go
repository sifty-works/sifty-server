package formread

import "context"

type Field struct {
	ID       string
	Label    string
	Kind     string
	Required bool
	Options  []string
}

type Form struct {
	URL    string
	System string
	Fields []Field
}

type FormReader interface {
	Read(ctx context.Context, url string) (Form, error)
}
