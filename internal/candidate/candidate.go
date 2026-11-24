package candidate

import (
	"strings"

	"github.com/sifty-works/sifty-server/internal/listing"
)

type ProfileData struct {
	Name               string        `json:"name"`
	Headline           string        `json:"headline,omitempty"`
	TargetRoles        []string      `json:"targetRoles,omitempty"`
	Keywords           []string      `json:"keywords,omitempty"`
	AllOf              []string      `json:"allOf,omitempty"`
	Compensation       *Compensation `json:"compensation,omitempty"`
	Engagement         []string      `json:"engagement,omitempty"`
	Hours              *Hours        `json:"hours,omitempty"`
	WorkModes          []string      `json:"workModes,omitempty"`
	PreferredLocations []Location    `json:"preferredLocations,omitempty"`
	Relocation         string        `json:"relocation,omitempty"`
	TimeZones          []string      `json:"timeZones,omitempty"`
	OtherRequirements  string        `json:"otherRequirements,omitempty"`
}

func Effective(p, base ProfileData) ProfileData {
	out := p
	if out.Compensation == nil {
		out.Compensation = base.Compensation
	}
	if len(out.Engagement) == 0 {
		out.Engagement = base.Engagement
	}
	if out.Hours == nil {
		out.Hours = base.Hours
	}
	if len(out.WorkModes) == 0 {
		out.WorkModes = base.WorkModes
	}
	if len(out.PreferredLocations) == 0 {
		out.PreferredLocations = base.PreferredLocations
	}
	return out
}

type GoalsView struct {
	TargetRoles                      []string
	Compensation                     *Compensation
	Engagement, WorkModes, TimeZones []string
	Hours                            *Hours
	PreferredLocations               []Location
}

func Goals(p ProfileData) GoalsView {
	return GoalsView{p.TargetRoles, p.Compensation, p.Engagement, p.WorkModes, p.TimeZones, p.Hours, p.PreferredLocations}
}

func InheritedFields(p, base ProfileData) []string {
	var out []string
	if p.Compensation == nil && base.Compensation != nil {
		out = append(out, "compensation")
	}
	if len(p.Engagement) == 0 && len(base.Engagement) > 0 {
		out = append(out, "engagement")
	}
	if p.Hours == nil && base.Hours != nil {
		out = append(out, "hours")
	}
	if len(p.WorkModes) == 0 && len(base.WorkModes) > 0 {
		out = append(out, "workModes")
	}
	if len(p.PreferredLocations) == 0 && len(base.PreferredLocations) > 0 {
		out = append(out, "preferredLocations")
	}
	return out
}

type Profile struct {
	TargetRoles []string
	Keywords    []string
	AllOf       []string
}

type Filter struct {
	SQL  string
	Args []any
}

func Visibility(p Profile) Filter {
	hasRoles := len(p.TargetRoles) > 0
	hasKeywords := len(p.Keywords) > 0

	if !hasRoles && !hasKeywords {
		return Filter{SQL: "1=0"}
	}

	var clauses []string
	var args []any

	if hasRoles {
		parts := make([]string, 0, len(p.TargetRoles))
		for _, r := range p.TargetRoles {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			part, roleArgs := roleClause(r)
			parts = append(parts, part)
			args = append(args, roleArgs...)
		}
		if len(parts) > 0 {
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
	}

	if hasKeywords {
		parts := make([]string, 0, len(p.Keywords))
		for _, kw := range p.Keywords {
			kw = strings.TrimSpace(kw)
			if kw == "" {
				continue
			}
			part, kwArgs := termClause(kw)
			parts = append(parts, part)
			args = append(args, kwArgs...)
		}
		if len(parts) > 0 {
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
	}

	for _, term := range p.AllOf {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		part, termArgs := termClause(term)
		clauses = append(clauses, part)
		args = append(args, termArgs...)
	}

	if len(clauses) == 0 {
		return Filter{SQL: "1=0"}
	}
	return Filter{SQL: strings.Join(clauses, " AND "), Args: args}
}

func NormalizeProfileData(pd *ProfileData) {
	pd.Compensation = normalizeCompensation(pd.Compensation)
	pd.Hours = normalizeHours(pd.Hours)
}

func NormalizeMeData(me *MeData) {
	me.Availability = normalizeAvailability(me.Availability)
	me.CurrentLocation = normalizeLocation(me.CurrentLocation)
	me.Demographics = normalizeDemographics(me.Demographics)
}

func normalizeCompensation(c *Compensation) *Compensation {
	if c == nil {
		return nil
	}
	c.DayRates = normalizeDayRates(c.DayRates)
	if c.CashMin == nil && c.CashTarget == nil && c.EquityOnTop == nil &&
		c.DayRates == nil && c.Currency == "" && c.GrossNet == "" && c.Period == "" && c.Crypto == "" {
		return nil
	}
	return c
}

func normalizeDayRates(dr *DayRates) *DayRates {
	if dr == nil {
		return nil
	}
	if dr.Min == nil && dr.Target == nil && dr.Currency == "" {
		return nil
	}
	return dr
}

func normalizeHours(h *Hours) *Hours {
	if h == nil {
		return nil
	}
	if h.Mode == "" && h.Weekly == nil && h.WeeklyMin == nil && h.WeeklyMax == nil {
		return nil
	}
	return h
}

func normalizeAvailability(a *Availability) *Availability {
	if a == nil {
		return nil
	}
	if a.NoticeValue == nil && a.NoticeUnit == "" && a.StartDate == "" {
		return nil
	}
	return a
}

func normalizeLocation(l *Location) *Location {
	if l == nil {
		return nil
	}
	if l.Country == "" && l.City == "" {
		return nil
	}
	return l
}

func normalizeDemographics(d *Demographics) *Demographics {
	if d == nil {
		return nil
	}
	if d.Gender == "" && d.GenderDescription == "" && len(d.Race) == 0 && d.RaceDescription == "" &&
		d.Ethnicity == "" && d.Disability == "" && d.VeteranStatus == "" && d.CriminalRecord == "" {
		return nil
	}
	return d
}

func roleClause(role string) (string, []any) {
	norm := listing.FTSNorm(role)
	if strings.TrimSpace(norm) == "" {
		return "1=0", nil
	}
	matchArg := `title: "` + strings.ReplaceAll(norm, `"`, `""`) + `"`
	return `v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?)`, []any{matchArg}
}

func termClause(term string) (string, []any) {
	norm := listing.FTSNorm(term)
	if strings.TrimSpace(norm) == "" {
		return "1=0", nil
	}
	matchArg := `{title tags text}: "` + strings.ReplaceAll(norm, `"`, `""`) + `"`
	return `v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?)`, []any{matchArg}
}
