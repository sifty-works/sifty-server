package reading

import (
	"strings"

	"github.com/sifty-works/sifty-server/internal/analysis"
)

type Vacancy struct {
	Title    string
	Company  string
	Location string
	Tags     []string
	Fields   map[string]string
	Text     string
	Full     bool
}

type Money = analysis.Money

type Hours = analysis.Hours

type VacancyReading struct {
	Title            string
	Full             bool
	Money            *Money
	WorkModes        []string
	Engagement       string
	Hours            *Hours
	Locations        []string
	TimeZone         string
	Requirements     []Requirement
	RequirementsRead bool
}

type Quote struct {
	Start int    `json:"start,omitempty"`
	End   int    `json:"end,omitempty"`
	Text  string `json:"text,omitempty"`
}

type Requirement struct {
	Text  string   `json:"text"`
	Kind  string   `json:"kind"`
	Level string   `json:"level"`
	Alts  []string `json:"alts,omitempty"`
	Quote Quote    `json:"quote,omitempty"`
}

type Condition struct {
	Value string `json:"value"`
	Quote string `json:"quote"`
}

type Record struct {
	Requirements []Requirement `json:"requirements,omitempty"`
	Money        *Condition    `json:"money,omitempty"`
	WorkMode     *Condition    `json:"work_mode,omitempty"`
	Engagement   *Condition    `json:"engagement,omitempty"`
	Location     *Condition    `json:"location,omitempty"`
	TimeZone     *Condition    `json:"timezone,omitempty"`
	Hours        *Condition    `json:"hours,omitempty"`
}

func Merge(code VacancyReading, rec Record) VacancyReading {
	r := code
	if r.Money == nil && rec.Money != nil && rec.Money.Value != "" {
		v := rec.Money.Value
		r.Money = analysis.ParseMoney(v, v)
	}
	if len(r.WorkModes) == 0 && rec.WorkMode != nil && rec.WorkMode.Value != "" {
		if m, ok := analysis.ParseMode(rec.WorkMode.Value); ok {
			r.addMode(m)
		}
	}
	if r.Engagement == "" && rec.Engagement != nil && rec.Engagement.Value != "" {
		r.Engagement = analysis.ParseEngagement(strings.ToLower(rec.Engagement.Value))
	}
	if len(r.Locations) == 0 && rec.Location != nil && rec.Location.Value != "" {
		r.addPlaces(rec.Location.Value)
	}
	if r.TimeZone == "" && rec.TimeZone != nil && rec.TimeZone.Value != "" {
		r.TimeZone = rec.TimeZone.Value
	}
	if r.Hours == nil && rec.Hours != nil && rec.Hours.Value != "" {
		r.Hours = analysis.ParseHours(rec.Hours.Value)
	}
	r.Requirements = rec.Requirements
	r.RequirementsRead = true
	return r
}

func FromCode(v Vacancy) VacancyReading {
	s := analysis.ReadSite(&analysis.Doc{Title: v.Title, Company: v.Company, Location: v.Location, Tags: v.Tags, Fields: v.Fields, Original: v.Text})
	return VacancyReading{
		Title: v.Title, Full: v.Full,
		Money: s.Money, WorkModes: s.Modes, Engagement: s.Engagement, Hours: s.Hours, Locations: s.Places, TimeZone: s.TimeZone,
	}
}

func (r *VacancyReading) addPlaces(value string) {
	s := analysis.Site{Modes: r.WorkModes, Places: r.Locations}
	s.AddPlaces(value)
	r.WorkModes, r.Locations = s.Modes, s.Places
}

func (r *VacancyReading) addMode(m string) {
	s := analysis.Site{Modes: r.WorkModes}
	s.AddMode(m)
	r.WorkModes = s.Modes
}
