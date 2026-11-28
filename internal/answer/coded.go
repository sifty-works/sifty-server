package answer

import (
	"context"
	"strings"

	"github.com/sifty-works/sifty-server/internal/formread"
)

const (
	noteNotInMyData = "Not in My data"
	noteConsent     = "Your consent"
	noteHowHeard    = "Left to you"
	noteFile        = "Attach this file to the form yourself"
	noteCV          = "Attach your CV to the form yourself"
	noteNoAnswerYet = "Sifty has no answer for this yet"
)

type Coded struct {
	Data Data
	Rest Rest
}

func (c Coded) Answer(ctx context.Context, v Vacancy, f formread.Form, have Answers) (Answers, error) {
	known, err := c.Data.Known(ctx)
	if err != nil {
		return nil, err
	}
	saved := savedQA(known)
	out := Answers{}
	var left []formread.Field
	for _, fl := range f.Fields {
		if h, ok := have[fl.ID]; ok && h.Source == SourceUser {
			out[fl.ID] = h
			continue
		}
		a, toRest, _ := answerField(fl, known, saved)
		if h, ok := have[fl.ID]; toRest && ok && len(h.Values) > 0 && (h.Source == SourceDrafted || h.Source == SourceMyData) {
			a, toRest = h, false
		}
		out[fl.ID] = a
		if toRest {
			left = append(left, fl)
		}
	}
	if c.Rest == nil || len(left) == 0 {
		return out, nil
	}
	got, err := c.Rest.Complete(ctx, v, known, formread.Form{URL: f.URL, System: f.System, Fields: left}, out)
	if err != nil {
		return out, err
	}
	for _, fl := range left {
		if a, ok := got[fl.ID]; ok {
			out[fl.ID] = a
		}
	}
	return out, nil
}

func fromMyData(fl formread.Field, key string, vals []Value, note string) Answer {
	evidence := "My data: " + strings.ReplaceAll(key, "_", " ")
	if !isChoice(fl.Kind, fl.Options) {
		return Answer{Values: []string{joinTexts(vals)}, Source: SourceMyData, Note: note, Evidence: evidence}
	}
	if picked, ok := pick(fl.Kind, fl.Options, vals); ok {
		return Answer{Values: picked, Source: SourceMyData, Note: note, Evidence: evidence}
	}
	return Answer{Note: "Profile says " + joinTexts(vals) + "; none of the options match"}
}

func savedQA(known Known) map[string]QA {
	saved := map[string]QA{}
	for _, q := range known.QA {
		if n := norm(q.Question); n != "" && strings.TrimSpace(q.Answer) != "" {
			saved[n] = q
		}
	}
	return saved
}

func Open(fl formread.Field, known Known) bool {
	if fl.Source == string(SourceMyData) {
		return false
	}
	_, _, open := answerField(fl, known, savedQA(known))
	return open
}

func leftOpen(key string) bool { return key == "" || vocab.drafted[key] }

func answerField(fl formread.Field, known Known, saved map[string]QA) (a Answer, toRest, open bool) {
	key := keyOf(fl.Label, fl.Kind, fl.Options)
	switch {
	case key == "resume":
		return Answer{Note: noteCV}, false, false
	case fl.Kind == formread.KindFile:
		return Answer{Note: noteFile}, false, false
	case key == "consent":
		return Answer{Note: noteConsent}, false, false
	case key == "how_heard":
		return Answer{Note: noteHowHeard}, false, false
	}
	authNote := ""
	vals := known.Values[key]
	if (key == "work_authorization" || key == "sponsorship") && known.Authorized != nil {
		if eligible, note, ok := known.Authorized(fl.Label); ok {
			yesNo := "No"
			if eligible == (key == "work_authorization") {
				yesNo = "Yes"
			}
			vals, authNote = []Value{{Text: yesNo}}, note
		}
	}
	if len(vals) > 0 {
		return fromMyData(fl, key, vals, authNote), false, false
	}
	if q, ok := saved[norm(fl.Label)]; ok && !vocab.sensitive[key] {
		val := Value{Text: strings.TrimSpace(q.Answer)}
		evidence := "Questions & Answers: " + q.Question
		if !isChoice(fl.Kind, fl.Options) {
			return Answer{Values: []string{val.Text}, Source: SourceQA, Evidence: evidence}, false, leftOpen(key)
		}
		if picked, ok := pick(fl.Kind, fl.Options, []Value{val}); ok {
			return Answer{Values: picked, Source: SourceQA, Evidence: evidence}, false, leftOpen(key)
		}
	}
	if leftOpen(key) {
		return Answer{Note: noteNoAnswerYet}, true, true
	}
	return Answer{Note: noteNotInMyData}, false, false
}
