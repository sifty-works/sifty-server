package answer

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/models"
)

const (
	noteBadChoice = "The drafted answer is not one of the options"
	noteNotAbout  = "Not drafted: this does not ask about you. It asks: "
)

type Drafter struct{ Model models.Model }

type question struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

type draftEntry struct {
	Key      string   `json:"key"`
	Values   []string `json:"values"`
	Evidence string   `json:"evidence"`
	Note     string   `json:"note"`
	Use      string   `json:"use"`
	Skip     bool     `json:"skip"`
}

type draftReply struct {
	Answers []draftEntry `json:"answers"`
}

const retryLine = "The previous reply left \"values\" empty for the questions below. \"values\" must hold the full text of the answer.\n"

func (d Drafter) Complete(ctx context.Context, v Vacancy, k Known, f formread.Form, _ Answers) (Answers, error) {
	out := Answers{}
	todo := f.Fields
	for round := 0; round < 2 && len(todo) > 0; round++ {
		retry := ""
		if round == 1 {
			retry = retryLine
		}
		reply, err := d.ask(ctx, v, k, todo, retry)
		if err != nil {
			if round == 0 || ctx.Err() != nil {
				return nil, err
			}
			break
		}
		todo = settle(reply, todo, k, out)
	}
	return out, nil
}

func (d Drafter) ask(ctx context.Context, v Vacancy, k Known, fields []formread.Field, retry string) (draftReply, error) {
	task := models.NamedTask("draft_answers")
	questions := make([]question, len(fields))
	for i, fl := range fields {
		questions[i] = question{fl.ID, fl.Label, fl.Kind, fl.Required, fl.Options}
	}
	asked, _ := json.Marshal(questions)
	var reply draftReply
	prompt := task.Prompt + "\n" + Context(v, k) + "\nCANDIDATE FIELDS WITH A VALUE\n" + usable(k) + "\n" + retry + "\nQUESTIONS\n" + string(asked) + "\n"
	_, err := task.Ask(ctx, d.Model, prompt, models.Asks, &reply)
	return reply, err
}

func settle(reply draftReply, fields []formread.Field, k Known, out Answers) []formread.Field {
	byKey := map[string]formread.Field{}
	for _, fl := range fields {
		byKey[fl.ID] = fl
	}
	done := map[string]bool{}
	for _, r := range reply.Answers {
		if fl, ok := byKey[r.Key]; ok && !done[fl.ID] {
			if a, ok := draftAnswer(fl, r, k); ok {
				out[fl.ID], done[fl.ID] = a, true
			}
		}
	}
	return slices.DeleteFunc(slices.Clone(fields), func(fl formread.Field) bool { return done[fl.ID] })
}

func draftAnswer(fl formread.Field, r draftEntry, k Known) (Answer, bool) {
	if vals := k.Values[r.Use]; r.Use != "" && len(vals) > 0 && !vocab.sensitive[r.Use] && !isChoice(fl.Kind, fl.Options) && fl.Kind != formread.KindYesNo {
		return fromMyData(fl, r.Use, vals, ""), true
	}
	var values []string
	for _, s := range r.Values {
		s = strings.TrimSpace(s)
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "Has:"), "Does not have:"))
		if s != "" {
			values = append(values, s)
		}
	}
	note := strings.TrimSpace(r.Note)
	switch {
	case r.Skip:
		return Answer{Note: noteNotAbout + note}, true
	case len(values) == 0:
		return Answer{}, false
	case isChoice(fl.Kind, fl.Options):
		wanted := make([]Value, len(values))
		for i, s := range values {
			wanted[i] = Value{Text: s}
		}
		if picked, ok := pick(fl.Kind, fl.Options, wanted); ok {
			return Answer{Values: picked, Source: SourceDrafted, Note: note, Evidence: r.Evidence}, true
		}
		return Answer{Note: noteBadChoice}, true
	}
	return Answer{Values: []string{strings.Join(values, " ")}, Source: SourceDrafted, Note: note, Evidence: r.Evidence}, true
}

func usable(k Known) string {
	var names []string
	for key, vals := range k.Values {
		if len(vals) > 0 && !vocab.sensitive[key] {
			names = append(names, key)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func Context(v Vacancy, k Known) string {
	clean := scrubber(k)
	var sb strings.Builder
	sb.WriteString("\nVACANCY\nTitle: " + v.Title + "\nCompany: " + v.Company + "\n\n" + v.Text + "\n\nCANDIDATE\n" + clean(k.Evidence) + "\n")
	var own []string
	for _, q := range k.QA {
		if key := keyOf(q.Question, "", nil); strings.TrimSpace(q.Answer) != "" && leftOpen(key) {
			own = append(own, "Q: "+q.Question+"\nA: "+q.Answer)
		}
	}
	if len(own) > 0 {
		sb.WriteString("\nCANDIDATE'S OWN ANSWERS\n" + clean(strings.Join(own, "\n\n")) + "\n")
	}
	return sb.String()
}

func scrubber(k Known) func(string) string {
	terms := slices.Clone(k.Contacts)
	for _, name := range k.Names {
		terms = append(terms, name)
		for _, w := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' && r != '-' }) {
			if len([]rune(w)) >= 3 {
				terms = append(terms, w)
			}
		}
	}
	var parts []string
	for _, t := range terms {
		if t = strings.TrimSpace(t); len([]rune(t)) >= 3 {
			p := regexp.QuoteMeta(t)
			if r := []rune(t); unicode.IsLetter(r[0]) || unicode.IsDigit(r[0]) {
				p = `\b` + p
			}
			if r := []rune(t); unicode.IsLetter(r[len(r)-1]) || unicode.IsDigit(r[len(r)-1]) {
				p += `\b`
			}
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return func(s string) string { return s }
	}
	slices.SortFunc(parts, func(a, b string) int { return len(b) - len(a) })
	re := regexp.MustCompile(`(?i)` + strings.Join(parts, "|"))
	return func(s string) string { return re.ReplaceAllString(s, "[removed]") }
}
