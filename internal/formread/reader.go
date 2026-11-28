package formread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sifty-works/sifty-server/internal/fetch"
)

var ErrNoTemplate = errors.New("formread: no form template for this address")

type Unreadable struct{ System, Reason string }

func (e Unreadable) Error() string {
	return fmt.Sprintf("formread: the %s form could not be read: %s", e.System, e.Reason)
}

type Reader struct {
	fetcher   fetch.Fetcher
	templates Templates
}

func NewReader(f fetch.Fetcher, ts Templates) *Reader {
	return &Reader{fetcher: f, templates: ts}
}

func (r *Reader) Read(ctx context.Context, page string) (Form, error) {
	t, dataURL, ok := r.templates.match(page)
	if !ok {
		return Form{}, ErrNoTemplate
	}
	resp, err := r.fetcher.Get(ctx, dataURL, fetch.Opt{Accept: "application/json"})
	if errors.Is(err, fetch.ErrNotFound) || errors.Is(err, fetch.ErrBlocked) {
		return Form{}, Unreadable{System: t.System, Reason: fetch.UserText(err)}
	}
	if err != nil {
		return Form{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(resp.Body))
	dec.UseNumber()
	var root any
	var fields []Field
	ok = dec.Decode(&root) == nil
	if ok {
		fields, ok = t.read(root)
	}
	if !ok {
		return Form{}, Unreadable{System: t.System, Reason: "The answer was not understood"}
	}
	return Form{URL: page, System: t.System, Fields: fields}, nil
}

func (t *Template) read(root any) ([]Field, bool) {
	var fields []Field
	found, bad := false, 0
	seen := map[string]int{}
	for _, q := range t.Questions {
		for _, set := range walk(root, q.List, t.Decode) {
			items, ok := set.([]any)
			found = found || ok
			for _, item := range items {
				get := func(path string) any { return first(item, path, t.Decode) }
				f := Field{Label: text(get(q.Label)), Required: get(q.Required) == true}
				if get(q.Hidden) == true {
					continue
				}
				if f.Label == "" {
					bad++
					continue
				}
				choices, _ := get(q.Options).([]any)
				for _, o := range choices {
					if s := text(first(o, q.OptionLabel, t.Decode)); s != "" {
						f.Options = append(f.Options, s)
					}
				}
				if f.Kind = t.kind(text(get(q.Type)), len(f.Options) > 0); f.Kind == kindSkip {
					continue
				}
				if f.ID = text(get(q.ID)); f.ID == "" {
					f.ID = f.Label
				}
				if seen[f.ID]++; seen[f.ID] > 1 {
					f.ID = fmt.Sprintf("%s#%d", f.ID, seen[f.ID])
				}
				fields = append(fields, f)
			}
		}
	}
	return fields, found && (len(fields) > 0 || bad == 0)
}

func (t *Template) kind(systemType string, hasOptions bool) string {
	if k, ok := t.Kinds[systemType]; ok {
		return k
	}
	if hasOptions {
		return "choice"
	}
	return "short_text"
}
