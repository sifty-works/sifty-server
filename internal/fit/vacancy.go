package fit

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/facts"
)

type Money struct {
	Min      float64 `json:"min,omitempty"`
	Max      float64 `json:"max,omitempty"`
	Currency string  `json:"currency,omitempty"`
	Period   string  `json:"period,omitempty"`
	Tax      string  `json:"tax,omitempty"`
}

type Hours struct {
	Mode string  `json:"mode,omitempty"`
	Min  float64 `json:"min,omitempty"`
	Max  float64 `json:"max,omitempty"`
}

type Requirement struct {
	Text  string
	Level string
}

type Vacancy struct {
	Title        string                      `json:"title"`
	Full         bool                        `json:"-"`
	Analysed     bool                        `json:"-"`
	Money        *Money                      `json:"money,omitempty"`
	WorkModes    []string                    `json:"workModes,omitempty"`
	Engagement   string                      `json:"engagement,omitempty"`
	Hours        *Hours                      `json:"hours,omitempty"`
	Locations    []string                    `json:"locations,omitempty"`
	TimeZone     string                      `json:"timeZone,omitempty"`
	Languages    []string                    `json:"languages,omitempty"`
	Requirements []Requirement               `json:"-"`
	Sources      map[facts.Kind]facts.Source `json:"-"`
	Decided      []RequirementLine           `json:"-"`
}

func FromFacts(title string, full bool, set facts.Set) Vacancy {
	v := Vacancy{Title: title, Full: full, Analysed: set.Analysed}
	var engagement []string
	add := func(xs *[]string, x string) {
		if !slices.Contains(*xs, x) {
			*xs = append(*xs, x)
		}
	}
	for _, f := range set.Facts {
		if old, ok := v.Sources[f.Kind]; !ok || weakSource(old) && !weakSource(sourceOf(f)) {
			if v.Sources == nil {
				v.Sources = map[facts.Kind]facts.Source{}
			}
			v.Sources[f.Kind] = sourceOf(f)
		}
		switch f.Kind {
		case facts.Money:
			if v.Money == nil && json.Unmarshal([]byte(f.Value), &v.Money) != nil {
				v.Money = nil
			}
		case facts.Hours:
			if v.Hours == nil && json.Unmarshal([]byte(f.Value), &v.Hours) != nil {
				v.Hours = nil
			}
		case facts.WorkMode:
			add(&v.WorkModes, f.Value)
		case facts.Engagement:
			add(&engagement, f.Value)
		case facts.Place:
			add(&v.Locations, f.Value)
		case facts.Language:
			if f.Flag == facts.Required {
				add(&v.Languages, f.Value)
			}
		case facts.TimeZone:
			if v.TimeZone == "" {
				v.TimeZone = f.Value
			}
		case facts.Requirement:
			if f.Flag != facts.Irrelevant {
				v.Requirements = append(v.Requirements, Requirement{Text: f.Value, Level: string(f.Flag)})
			}
		}
	}
	v.Engagement = strings.Join(engagement, " ")
	return v
}

func sourceOf(f facts.Fact) facts.Source {
	if f.End > f.Start {
		return facts.Source{Way: facts.Vocabulary, Origin: f.Source.Origin}
	}
	return f.Source
}
