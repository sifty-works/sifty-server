package fit

import (
	"encoding/json"
	"slices"
	"strconv"
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

type Judged struct {
	Required bool
	Verdict  string
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
	Years        int                         `json:"-"`
	Seniority    string                      `json:"-"`
	Stage        string                      `json:"-"`
	Deadline     string                      `json:"-"`
	Length       string                      `json:"-"`
	Clearance    string                      `json:"-"`
	Relocation   string                      `json:"-"`
	Visa         string                      `json:"-"`
	Requirements []Requirement               `json:"-"`
	Sources      map[facts.Kind]facts.Source `json:"-"`
	Judged       []Judged                    `json:"-"`
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
		case facts.Years:
			if v.Years == 0 {
				v.Years, _ = strconv.Atoi(f.Value)
			}
		case facts.Seniority:
			if v.Seniority == "" {
				v.Seniority = f.Value
			}
		case facts.CompanyStage:
			if v.Stage == "" {
				v.Stage = f.Value
			}
		case facts.Deadline:
			if v.Deadline == "" {
				v.Deadline = f.Value
			}
		case facts.ContractLen:
			if v.Length == "" {
				v.Length = f.Value
			}
		case facts.Clearance:
			if v.Clearance == "" {
				v.Clearance = f.Value
			}
		case facts.Relocation:
			if v.Relocation == "" {
				v.Relocation = f.Value
			}
		case facts.Visa:
			if v.Visa == "" {
				v.Visa = f.Value
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
