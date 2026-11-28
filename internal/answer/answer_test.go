package answer_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/answer"
	"github.com/sifty-works/sifty-server/internal/formread"
)

type fakeData struct{ known answer.Known }

func (d fakeData) Known(context.Context) (answer.Known, error) { return d.known, nil }

type countingRest struct {
	calls  int
	fields []string
}

func (r *countingRest) Complete(_ context.Context, _ answer.Vacancy, _ answer.Known, f formread.Form, _ answer.Answers) (answer.Answers, error) {
	r.calls++
	out := answer.Answers{}
	for _, fl := range f.Fields {
		r.fields = append(r.fields, fl.ID)
		out[fl.ID] = answer.Answer{Values: []string{"drafted"}, Source: answer.SourceDrafted}
	}
	return out, nil
}

func person() answer.Known {
	return answer.Known{
		Values: map[string][]answer.Value{
			"full_name":  {{Text: "Rowan Quill"}},
			"first_name": {{Text: "Rowan"}},
			"email":      {{Text: "rowan@example.test"}},
			"phone":      {{Text: "+000 000 0000"}},
			"linkedin":   {{Text: "https://linkedin.example.test/in/rowan"}},
		},
		Authorized: func(label string) (bool, string, bool) {
			switch {
			case strings.Contains(label, "United States") || strings.Contains(label, "U.S."):
				return true, "", true
			case strings.Contains(label, "Japan"):
				return false, "My data has no work authorization for Japan", true
			}
			return false, "", false
		},
		QA: []answer.QA{{Question: "Why do you want to work here?", Answer: "I like the product."}},
	}
}

func run(t *testing.T, known answer.Known, fields []formread.Field, have answer.Answers) (answer.Answers, *countingRest) {
	t.Helper()
	rest := &countingRest{}
	out, err := answer.Coded{Data: fakeData{known}, Rest: rest}.Answer(context.Background(), answer.Vacancy{}, formread.Form{Fields: fields}, have)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	return out, rest
}

func TestKnownFieldsAreAnsweredByCodeAndNeverReachTheModel(t *testing.T) {
	out, rest := run(t, person(), []formread.Field{
		{ID: "e", Label: "Email*", Kind: "short_text"},
		{ID: "p", Label: "Phone number", Kind: "short_text"},
		{ID: "l", Label: "LinkedIn Profile", Kind: "short_text"},
		{ID: "f", Label: "First Name", Kind: "short_text"},
		{ID: "q", Label: "Why do you want to work here?", Kind: "long_text"},
		{ID: "o", Label: "Describe a hard bug you fixed", Kind: "long_text"},
		{ID: "a", Label: "Are you legally authorized to work in the United States?", Kind: "choice", Options: []string{"Yes", "No"}},
		{ID: "s", Label: "Will you now or in the future require visa sponsorship to work in the U.S.?", Kind: "yes_no"},
		{ID: "j", Label: "Are you legally authorized to work in Japan?", Kind: "yes_no"},
		{ID: "where", Label: "Are you authorized to work in the location where this role is based?", Kind: "yes_no"},
	}, nil)
	want := map[string]string{"j": "No", "e": "rowan@example.test", "p": "+000 000 0000", "l": "https://linkedin.example.test/in/rowan", "f": "Rowan", "q": "I like the product.", "a": "Yes", "s": "No"}
	for id, v := range want {
		if got := out[id].Values; len(got) != 1 || got[0] != v {
			t.Errorf("field %s = %v, want %q", id, got, v)
		}
	}
	if out["q"].Source != answer.SourceQA || out["e"].Source != answer.SourceMyData {
		t.Errorf("sources: q=%q e=%q", out["q"].Source, out["e"].Source)
	}
	if rest.calls != 1 || strings.Join(rest.fields, ",") != "o,where" {
		t.Errorf("model got calls=%d fields=%v, want one call with the open question and the question that names no place", rest.calls, rest.fields)
	}
	if !strings.Contains(out["j"].Note, "no work authorization for Japan") {
		t.Errorf("note for a country without authorization = %q", out["j"].Note)
	}
}

func TestGenderIsAnsweredOnlyWhenTheUserFilledIt(t *testing.T) {
	field := formread.Field{ID: "g", Label: "Gender", Kind: "choice", Options: []string{"Woman", "Man", "Non-binary", "Prefer not to say"}}
	filled := person()
	filled.Values["gender"] = []answer.Value{{Text: "Non-binary", Match: []string{"non_binary", "Non-binary"}}}
	out, _ := run(t, filled, []formread.Field{field}, nil)
	if got := out["g"].Values; len(got) != 1 || got[0] != "Non-binary" {
		t.Errorf("filled gender = %v, want Non-binary", got)
	}

	unfilled := []formread.Field{field}
	for i, label := range []string{
		"How do you identify? (gender identity)",
		"How do you identify? (race/ethnicity)",
		"If you are based in the US, what is your veteran status?",
		"Do you have a physical or mental disability, impairment, or condition?",
	} {
		unfilled = append(unfilled, formread.Field{ID: fmt.Sprint("x", i), Label: label, Kind: "choice", Options: []string{"Yes", "No", "Prefer not to say"}})
	}
	out, rest := run(t, person(), unfilled, nil)
	for _, fl := range unfilled {
		if len(out[fl.ID].Values) != 0 {
			t.Errorf("unfilled %q was answered: %v", fl.Label, out[fl.ID].Values)
		}
	}
	if rest.calls != 0 {
		t.Errorf("unfilled sensitive fields went to the model: %v", rest.fields)
	}
}

func TestAnswerEditedByUserComesBackUnchanged(t *testing.T) {
	edited := answer.Answer{Values: []string{"rowan.work@example.test"}, Source: answer.SourceUser}
	out, _ := run(t, person(), []formread.Field{{ID: "e", Label: "Email", Kind: "short_text"}}, answer.Answers{"e": edited})
	if got := out["e"]; got.Source != answer.SourceUser || len(got.Values) != 1 || got.Values[0] != "rowan.work@example.test" {
		t.Errorf("edited answer = %+v, want it unchanged", got)
	}
}

func TestChoiceWhoseStoredValueMatchesNoOptionGetsNoAnswerAndNote(t *testing.T) {
	known := person()
	known.Values["country"] = []answer.Value{{Text: "Narnia"}}
	out, _ := run(t, known, []formread.Field{{ID: "c", Label: "Country", Kind: "choice", Options: []string{"Germany", "France", "Spain"}}}, nil)
	got := out["c"]
	if len(got.Values) != 0 {
		t.Errorf("answer = %v, want none", got.Values)
	}
	if !strings.Contains(got.Note, "Profile says Narnia") || !strings.Contains(got.Note, "none of the options match") {
		t.Errorf("note = %q", got.Note)
	}
}
