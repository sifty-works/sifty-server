package fit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/places"
)

type comparer struct {
	name    string
	compare func(v Vacancy, p ProfileView, s Strictness) Verdict
}

func (c comparer) Name() string { return c.name }

func (c comparer) Compare(v Vacancy, p ProfileView, s Strictness) Verdict {
	return c.compare(v, p, s)
}

var Comparers = []Comparer{
	comparer{"role", compareRole},
	comparer{"money", compareMoney},
	comparer{"location", compareLocation},
	comparer{"remote_restriction", compareAuthorization},
	comparer{"time_zone", compareTimeZone},
	comparer{"work_mode", compareWorkMode},
	comparer{"engagement", compareEngagement},
	comparer{"hours", compareHours},
	comparer{"languages", compareLanguages},
	comparer{"requirements", compareRequirements},
}

var musts = []string{"work_mode", "languages", "remote_restriction"}

func strictnessOf(name string) Strictness {
	if slices.Contains(musts, name) {
		return Must
	}
	return Prefer
}

type geo struct {
	places    []places.Place
	countries []string
	remote    bool
	open      bool
	anywhere  bool
	known     bool
}

func geoOf(v Vacancy) geo {
	g := geo{remote: has(v.WorkModes, "remote"), known: true}
	g.open = g.remote
	worldwide := false
	for _, l := range v.Locations {
		pl := places.Resolve(l)
		g.places = append(g.places, pl)
		g.open = g.open && pl.Worldwide
		worldwide = worldwide || pl.Worldwide
		g.known = g.known && pl.Known()
		g.countries = append(g.countries, pl.Countries...)
	}
	g.anywhere = g.open && worldwide && v.TimeZone == ""
	return g
}

func result(u float64, src facts.Source, s Strictness, match, mismatch, unknown string) Verdict {
	switch {
	case u < 0:
		r := Verdict{Outcome: Unknown, Source: src}
		if s == Must {
			r.Reason = unknown
		}
		return r
	case u >= 1:
		return Verdict{Outcome: Match, Utility: u, Source: src, Reason: match}
	}
	return Verdict{Outcome: Mismatch, Utility: u, Source: src, Reason: mismatch}
}

func skipped() Verdict { return Verdict{Outcome: Skipped} }

func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func either(xs []string) string { return strings.ReplaceAll(list(xs, ""), " · ", " or ") }

func compareRole(v Vacancy, p ProfileView, s Strictness) Verdict {
	if len(p.Goals.TargetRoles) == 0 {
		return skipped()
	}
	u := role(p.Goals.TargetRoles, v.Title)
	r := result(u, facts.Source{}, s, "", "Partly matches your target roles", "")
	switch {
	case r.Outcome == Mismatch && u >= 0.5:
		r.Outcome, r.Reason = Match, ""
	case r.Outcome == Mismatch && u == 0:
		r.Reason = fmt.Sprintf("%q is far from your target roles", v.Title)
	}
	return r
}

func compareMoney(v Vacancy, p ProfileView, s Strictness) Verdict {
	c := p.Goals.Compensation
	if c == nil || c.CashMin == nil || *c.CashMin <= 0 {
		return skipped()
	}
	u := money(c, v.Money, p.Rates)
	if u < 0 {
		return result(u, facts.Source{}, s, "", "", "")
	}
	pays := "Pays " + moneyValue(v.Money)
	goal := strings.TrimSpace(c.Currency + " " + amount(*c.CashMin) + per(c.Period) + " " + c.GrossNet)
	return result(u, v.Sources[facts.Money], s, pays+" — meets your minimum", pays+" — below your minimum of "+goal, "")
}

func compareLocation(v Vacancy, p ProfileView, s Strictness) Verdict {
	prefs := p.Goals.PreferredLocations
	if len(prefs) == 0 {
		return skipped()
	}
	g := geoOf(v)
	where, _ := places.Short(v.Locations)
	if g.anywhere {
		where = "Remote, anywhere"
	}
	line := sentence(where) + " — you prefer " + wanted(prefs)
	return result(location(prefs, g.places, g.anywhere), v.Sources[facts.Place], s, line, line, "")
}

func wanted(prefs []candidate.Location) string {
	var want []string
	for _, l := range prefs {
		want = append(want, strings.Trim(l.City+", "+l.Country, ", "))
	}
	return strings.Join(want, " · ")
}

func compareAuthorization(v Vacancy, p ProfileView, s Strictness) Verdict {
	g := geoOf(v)
	if !g.remote {
		return skipped()
	}
	where, _ := places.Short(v.Locations)
	return result(restriction(p.Authorizations, g.places, g.remote, g.anywhere, g.known), v.Sources[facts.Place], s,
		"Remote work is open to you there", "Remote only in "+where+" — you need a visa there", "Work authorization not stated")
}

func compareTimeZone(v Vacancy, p ProfileView, s Strictness) Verdict {
	if len(p.Goals.TimeZones) == 0 {
		return skipped()
	}
	zones := places.ZoneBands(v.TimeZone)
	if zones == nil {
		zones = places.BandsOf(geoOf(v).countries)
	}
	var want []string
	for _, b := range places.Bands() {
		if has(p.Goals.TimeZones, b.Code) {
			want = append(want, b.Label)
		}
	}
	line := "Time zone fits " + strings.Join(want, " · ")
	return result(overlap(p.Goals.TimeZones, zones), v.Sources[facts.TimeZone], s, line, "Time zone is outside "+strings.Join(want, " · "), "")
}

func compareWorkMode(v Vacancy, p ProfileView, s Strictness) Verdict {
	want := p.Goals.WorkModes
	if len(want) == 0 {
		return skipped()
	}
	g := geoOf(v)
	u := overlap(want, v.WorkModes)
	if u == 1 && g.remote && !g.open && has(want, "remote") {
		return Verdict{Outcome: Unknown}
	}
	r := result(u, v.Sources[facts.WorkMode], s,
		sentence(either(v.WorkModes))+" — you want "+either(want),
		sentence(either(v.WorkModes))+" only — you want "+either(want), "Work mode not stated")
	if r.Outcome == Mismatch && p.Relocation == "yes" && !g.remote {
		r.Soft, r.Reason = true, r.Reason+", open to relocation"
	}
	return r
}

func compareEngagement(v Vacancy, p ProfileView, s Strictness) Verdict {
	want := p.Goals.Engagement
	if len(want) == 0 {
		return skipped()
	}
	stated := sentence(either(strings.Fields(v.Engagement)))
	return result(overlap(want, strings.Fields(v.Engagement)), v.Sources[facts.Engagement], s,
		stated+" — you want "+either(want), stated+" only — you want "+either(want), "Engagement not stated")
}

func compareHours(v Vacancy, p ProfileView, s Strictness) Verdict {
	h := p.Goals.Hours
	if h == nil || h.Mode == "" {
		return skipped()
	}
	u := hours(h, v.Hours)
	if u == 0 && v.Hours.Mode == h.Mode {
		return result(u, v.Sources[facts.Hours], s, "", "Weekly hours do not overlap yours", "")
	}
	line := "Not stated"
	if v.Hours != nil {
		line = sentence(list([]string{v.Hours.Mode}, "")) + " — you want " + list([]string{h.Mode}, "")
	}
	return result(u, v.Sources[facts.Hours], s, line, line, "")
}

func compareLanguages(v Vacancy, p ProfileView, s Strictness) Verdict {
	if len(v.Languages) == 0 {
		return skipped()
	}
	need := "Requires " + strings.Join(v.Languages, ", ")
	if len(p.Languages) == 0 {
		return result(-1, v.Sources[facts.Language], s, "", "", need+" — your languages are not listed")
	}
	var missing []string
	for _, l := range v.Languages {
		if !slices.ContainsFunc(p.Languages, func(x candidate.Language) bool { return has([]string{x.Name}, l) }) {
			missing = append(missing, l)
		}
	}
	u := 1.0
	if len(missing) > 0 {
		u, need = 0, "Requires "+strings.Join(missing, ", ")
	}
	return result(u, v.Sources[facts.Language], s, need+" — in your languages", need+" — not in your languages", "")
}

func compareRequirements(v Vacancy, p ProfileView, s Strictness) Verdict {
	if len(v.Decided) == 0 {
		return skipped()
	}
	var num, den float64
	no := 0
	for _, l := range v.Decided {
		w := 1.0
		if l.Level == "required" {
			w = 2.0
		}
		switch l.Verdict {
		case "✓":
			num, den = num+w, den+w
		case "✕":
			den, no = den+w, no+1
		}
	}
	if den == 0 {
		return result(-1, facts.Source{}, s, "", "", "")
	}
	u := num / den
	if no > 0 {
		return Verdict{Outcome: Mismatch, Utility: u, Reason: fmt.Sprintf("%d of the requirements are not met", no)}
	}
	return Verdict{Outcome: Match, Utility: u, Reason: "Requirements met"}
}

func moneyValue(m *Money) string {
	return symbol(m.Currency) + cash(m.Min, m.Max) + per(m.Period)
}

func moneyTile(v Vacancy, _ geo, rates map[string]float64) (string, string, string) {
	m := v.Money
	if m == nil {
		return "Not stated", "", ""
	}
	if eur := convert(m.Max, m, "EUR", "year", rates); eur > 0 {
		return moneyValue(m), "≈ €" + amount(rounded(eur)), ""
	}
	return moneyValue(m), "", ""
}

func locationTile(v Vacancy, g geo, _ map[string]float64) (string, string, string) {
	switch {
	case len(v.Locations) > 0:
		short, full := places.Short(v.Locations)
		return short, "", full
	case g.anywhere:
		return "Remote, anywhere", "", ""
	}
	return "Not stated", "", ""
}

func contractTile(v Vacancy, _ geo, _ map[string]float64) (string, string, string) {
	engagement := v.Engagement
	if engagement == "" {
		engagement = "Engagement not stated"
	}
	contract := []string{engagement}
	if h := v.Hours; h != nil {
		contract = append(contract, h.Mode, strings.Trim(amount(h.Min)+"–"+amount(h.Max), "–")+map[bool]string{true: " h/week"}[h.Max > 0])
	}
	return list(contract, "Not stated"), "", ""
}
