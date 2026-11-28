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
	saved := map[string]QA{}
	for _, q := range known.QA {
		if n := norm(q.Question); n != "" && strings.TrimSpace(q.Answer) != "" {
			saved[n] = q
		}
	}
	out := Answers{}
	var left []formread.Field
	for _, fl := range f.Fields {
		if h, ok := have[fl.ID]; ok && h.Source == SourceUser {
			out[fl.ID] = h
			continue
		}
		a, toRest := answerField(fl, known, saved)
		out[fl.ID] = a
		if toRest {
			left = append(left, fl)
		}
	}
	if c.Rest == nil || len(left) == 0 {
		return out, nil
	}
	got, err := c.Rest.Complete(ctx, v, formread.Form{URL: f.URL, System: f.System, Fields: left}, out)
	if err != nil {
		return nil, err
	}
	for _, fl := range left {
		if a, ok := got[fl.ID]; ok {
			out[fl.ID] = a
		}
	}
	return out, nil
}

func answerField(fl formread.Field, known Known, saved map[string]QA) (a Answer, toRest bool) {
	key := keyOf(fl.Label, fl.Kind, fl.Options)
	switch {
	case key == "resume":
		return Answer{Note: noteCV}, false
	case fl.Kind == formread.KindFile:
		return Answer{Note: noteFile}, false
	case key == "consent":
		return Answer{Note: noteConsent}, false
	case key == "how_heard":
		return Answer{Note: noteHowHeard}, false
	}
	authNote := ""
	vals, evidence := known.Values[key], "My data: "+strings.ReplaceAll(key, "_", " ")
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
		if !isChoice(fl.Kind, fl.Options) {
			return Answer{Values: []string{joinTexts(vals)}, Source: SourceMyData, Note: authNote, Evidence: evidence}, false
		}
		if picked, ok := pick(fl.Kind, fl.Options, vals); ok {
			return Answer{Values: picked, Source: SourceMyData, Note: authNote, Evidence: evidence}, false
		}
		return Answer{Note: "Profile says " + joinTexts(vals) + "; none of the options match"}, false
	}
	if q, ok := saved[norm(fl.Label)]; ok && !vocab.sensitive[key] {
		val := Value{Text: strings.TrimSpace(q.Answer)}
		evidence := "Questions & Answers: " + q.Question
		if !isChoice(fl.Kind, fl.Options) {
			return Answer{Values: []string{val.Text}, Source: SourceQA, Evidence: evidence}, false
		}
		if picked, ok := pick(fl.Kind, fl.Options, []Value{val}); ok {
			return Answer{Values: picked, Source: SourceQA, Evidence: evidence}, false
		}
	}
	if key == "" || vocab.drafted[key] {
		return Answer{Note: noteNoAnswerYet}, true
	}
	return Answer{Note: noteNotInMyData}, false
}
