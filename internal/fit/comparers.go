package fit

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

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
	comparer{"domain", compareDomain},
	comparer{"hours", compareHours},
	comparer{"languages", compareLanguages},
	comparer{"requirements", compareRequirements},
}

var musts = []string{"languages", "remote_restriction"}

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
	b := p.Goals.Locations
	if b.IsZero() {
		return skipped()
	}
	g := geoOf(v)
	var hits []hit
	for i, pl := range g.places {
		hits = append(hits, hit{v.Locations[i], placeBucket(b, pl)})
	}
	if g.anywhere && len(b.Preferred) > 0 {
		hits = []hit{{"Remote, anywhere", candidate.Preferred}}
	}
	return bucketVerdict(hits, v.Sources[facts.Place], "")
}

func compareAuthorization(v Vacancy, p ProfileView, s Strictness) Verdict {
	g := geoOf(v)
	if !g.remote {
		return Verdict{Outcome: Skipped, Reason: "Checked for remote vacancies only"}
	}
	where := places.Names(v.Locations)
	u, listed := restriction(p.Authorizations, g.places, g.anywhere, g.known)
	mismatch, unknown := "Remote only in "+where+" — not in your work authorizations", "Work authorization not stated"
	if listed {
		mismatch = "Remote only in " + where + " — you need a visa there"
	}
	if g.known && len(g.countries) > 0 && len(p.Authorizations) == 0 {
		unknown = "Add your work authorizations to the profile"
	}
	return result(u, v.Sources[facts.Place], s, "Remote work is open to you there", mismatch, unknown)
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
	b := p.Goals.WorkMode
	if b.IsZero() {
		return skipped()
	}
	return bucketVerdict(valueHits(b, v.WorkModes), v.Sources[facts.WorkMode], "Work mode not stated")
}

func compareEngagement(v Vacancy, p ProfileView, s Strictness) Verdict {
	b := p.Goals.EngagementType
	if b.IsZero() {
		return skipped()
	}
	return bucketVerdict(valueHits(b, strings.Fields(v.Engagement)), v.Sources[facts.Engagement], "Engagement not stated")
}

func compareDomain(v Vacancy, p ProfileView, s Strictness) Verdict {
	b := p.Goals.Domain
	if b.IsZero() {
		return skipped()
	}
	labels := domainLabels(v.Domains)
	hits := make([]hit, len(labels))
	for i, d := range v.Domains {
		hits[i] = hit{labels[i], b.Of(d)}
	}
	return bucketVerdict(hits, v.Sources[facts.Domain], "Domain not stated")
}

func compareHours(v Vacancy, p ProfileView, s Strictness) Verdict {
	b := p.Goals.WorkingTime
	if b.IsZero() {
		return skipped()
	}
	var modes []string
	if v.Hours != nil && v.Hours.Mode != "" {
		modes = []string{v.Hours.Mode}
	}
	r := bucketVerdict(valueHits(b, modes), v.Sources[facts.Hours], "")
	if (r.Bucket == candidate.Preferred || r.Bucket == candidate.Possible) && weekly(p.Goals.Hours, v.Hours) == 0 {
		return Verdict{Outcome: Mismatch, Source: r.Source, Reason: "Weekly hours do not overlap yours"}
	}
	return r
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
	if len(v.Judged) == 0 {
		return skipped()
	}
	var num, den float64
	no, near := 0, 0
	for _, j := range v.Judged {
		w := 1.0
		if j.Required {
			w = 2.0
		}
		switch j.Verdict {
		case "✓":
			num, den = num+w, den+w
		case "≈":
			num, den, near = num+w/2, den+w, near+1
		case "✕":
			den, no = den+w, no+1
		}
	}
	if den == 0 {
		return result(-1, facts.Source{}, s, "", "", "")
	}
	u := num / den
	switch {
	case no > 0:
		return Verdict{Outcome: Mismatch, Utility: u, Reason: fmt.Sprintf("%d of the requirements are not met", no)}
	case near > 0:
		return Verdict{Outcome: Match, Utility: u, Reason: fmt.Sprintf("%d of the requirements are close", near)}
	}
	return Verdict{Outcome: Match, Utility: u, Reason: "Requirements met"}
}

func moneyValue(m *Money) string {
	return symbol(m.Currency) + cash(m.Min, m.Max) + per(m.Period)
}

func moneyTile(v Vacancy, _ geo, rates map[string]float64) (string, string, string) {
	m := v.Money
	if m == nil {
		return notStated, "", ""
	}
	if eur := convert(m.Max, m, "EUR", "year", rates); eur > 0 {
		return moneyValue(m), "≈ €" + amount(rounded(eur)), ""
	}
	return moneyValue(m), "", ""
}

func timeZoneTile(v Vacancy, g geo, _ map[string]float64) (string, string, string) {
	if v.TimeZone != "" {
		return v.TimeZone, "", ""
	}
	var zones []string
	for _, b := range places.Bands() {
		if has(places.BandsOf(g.countries), b.Code) {
			zones = append(zones, b.Label)
		}
	}
	return list(zones, notStated), "", ""
}

func authorizationTile(v Vacancy, g geo, _ map[string]float64) (string, string, string) {
	if !g.remote {
		return "Not checked", "", ""
	}
	if g.anywhere {
		return "No restriction", "", ""
	}
	short, full := places.Short(v.Locations)
	return list([]string{short}, notStated), "", full
}

func locationTile(v Vacancy, g geo, _ map[string]float64) (string, string, string) {
	switch {
	case len(v.Locations) > 0:
		short, full := places.Short(v.Locations)
		return short, "", full
	case g.anywhere:
		return "Remote, anywhere", "", ""
	}
	return notStated, "", ""
}

func contractTile(v Vacancy, _ geo, _ map[string]float64) (string, string, string) {
	return list(strings.Fields(v.Engagement), notStated), "", ""
}

func hoursTile(v Vacancy, _ geo, _ map[string]float64) (string, string, string) {
	h := v.Hours
	if h == nil {
		return notStated, "", ""
	}
	return list([]string{h.Mode, strings.Trim(amount(h.Min)+"–"+amount(h.Max), "–") + map[bool]string{true: " h/week"}[h.Max > 0]}, notStated), "", ""
}

func languagesTile(v Vacancy, _ geo, _ map[string]float64) (string, string, string) {
	return list(v.Languages, notStated), "", ""
}

const shortfallYears = 2

func keyFactTiles(v Vacancy, p ProfileView, cs []Criterion) []Tile {
	var out []Tile
	if v.Years > 0 {
		out = append(out, experienceTile(v, p))
	}
	for _, t := range []Tile{{Name: "Seniority", Value: v.Seniority}, {Name: "Company stage", Value: v.Stage}} {
		if t.Value != "" {
			t.Verdict = neutral
			out = append(out, t)
		}
	}
	return append(append(out, domainTile(v, p, cs)...), conditionTiles(v, p.Today)...)
}

func domainLabels(keys []string) []string {
	labels := make([]string, len(keys))
	for i, k := range keys {
		labels[i] = facts.DomainTitle(k)
	}
	return labels
}

func domainTile(v Vacancy, p ProfileView, cs []Criterion) []Tile {
	goal := !p.Goals.Domain.IsZero()
	if len(v.Domains) == 0 && !goal {
		return nil
	}
	t := Tile{Name: "Domain", Value: notStated, Verdict: neutral}
	if len(v.Domains) > 0 {
		t.Value, t.Line = strings.Join(domainLabels(v.Domains), " · "), "Read by the model"
	}
	if goal {
		line, verdict, bucket := tileLine(cs, "domain", len(v.Domains) > 0)
		if len(v.Domains) > 0 {
			line += " · read by the model"
		}
		t.Verdict, t.Bucket, t.Line = verdict, bucket, line
	}
	return []Tile{t}
}

func conditionTiles(v Vacancy, today time.Time) []Tile {
	var out []Tile
	if v.Deadline != "" {
		out = append(out, applyByTile(v.Deadline, today))
	}
	if v.Length != "" {
		out = append(out, Tile{Name: "Contract length", Value: v.Length, Verdict: neutral})
	}
	for _, t := range []Tile{
		{Name: "Citizenship or clearance", Value: v.Clearance},
		{Name: "Relocation", Value: map[string]string{"offered": "Offered", "none": "Not offered"}[v.Relocation]},
		{Name: "Visa", Value: map[string]string{"offered": "Sponsorship offered", "refused": "No sponsorship"}[v.Visa]},
	} {
		if t.Value != "" {
			t.Verdict = neutral
			out = append(out, t)
		}
	}
	return out
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

func applyByTile(deadline string, today time.Time) Tile {
	t := Tile{Name: "Apply by", Value: deadline, Verdict: neutral}
	date, err := time.Parse("2006-01-02", deadline)
	if err != nil {
		return t
	}
	t.Value = date.Format("2 Jan 2006")
	if today.IsZero() {
		return t
	}
	y, m, d := today.Date()
	days := int(date.Sub(time.Date(y, m, d, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	switch {
	case days > 0:
		t.Line = plural(days, "day") + " left"
	case days == 0:
		t.Line = "Closes today"
	case days == -1:
		t.Line, t.Verdict = "Closed yesterday", "✕"
	default:
		t.Line, t.Verdict = "Closed "+plural(-days, "day")+" ago", "✕"
	}
	return t
}

func experienceTile(v Vacancy, p ProfileView) Tile {
	t := Tile{Name: "Experience", Value: strconv.Itoa(v.Years) + "+ years", Verdict: "?"}
	have := p.DerivedYears.Total
	if have <= 0 {
		t.Line = "No experience in your data"
		return t
	}
	t.Line = "You have " + yearsEvidence("", p.DerivedYears)
	switch {
	case have >= v.Years:
		t.Verdict = "✓"
	case v.Years-have > shortfallYears:
		t.Verdict = "✕"
	}
	return t
}
