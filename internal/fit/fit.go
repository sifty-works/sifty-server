package fit

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/places"
	"github.com/sifty-works/sifty-server/internal/reading"
	"github.com/sifty-works/sifty-server/internal/skills"
)

type ProfileView struct {
	Goals          candidate.GoalsView
	Authorizations []candidate.WorkAuthorization
	Skills         []candidate.Skill
	AbsentSkills   []candidate.AbsentSkill
	Confirmed      []candidate.ConfirmedFact
	DerivedYears   candidate.DerivedYears
}

type RequirementLine struct {
	Index       int      `json:"index"`
	Text        string   `json:"text"`
	Alts        []string `json:"alts,omitempty"`
	Level       string   `json:"level"`
	Verdict     string   `json:"verdict"`
	Evidence    string   `json:"evidence,omitempty"`
	NamedSkills []string `json:"namedSkills,omitempty"`
}

type Rules struct{ Weights, Rates map[string]float64 }

var Criteria = []string{"role", "money", "location", "remote_restriction", "work_mode", "engagement", "hours", "time_zone", "requirements"}

const (
	Scored  = "scored"
	NoText  = "no_text"
	NoGoals = "no_goals"
)

var Texts = map[string]string{
	"":      "Not compared yet",
	NoText:  "Full job description is unavailable",
	NoGoals: "Your profile states no goals to compare",
}

const (
	StrongFrom  = 75
	PartialFrom = 55
)

type Criterion struct {
	Name            string
	Weight, Utility float64
	Known           bool
}

type Tile struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Converted string `json:"converted,omitempty"`
	Line      string `json:"line"`
	Verdict   string `json:"verdict"`
}

type Fit struct {
	State                  string
	Score                  int
	Bucket                 string
	Text                   string
	Criteria               []Criterion
	Tiles                  []Tile
	ReqRead                bool
	ReqOK, ReqNo, ReqMaybe int
	Requirements           []RequirementLine
}

const na = -1.0

var tiles = []struct {
	name     string
	criteria []string
}{{"Money", []string{"money"}}, {"Location", []string{"location", "remote_restriction", "time_zone"}},
	{"Work mode", []string{"work_mode"}}, {"Contract", []string{"engagement", "hours"}}}

func Score(p ProfileView, v reading.VacancyReading, r Rules) Fit {
	g := p.Goals
	var ps []places.Place
	open, known := has(v.WorkModes, "remote"), true
	var zoneCountries []string
	for _, l := range v.Locations {
		pl := places.Resolve(l)
		ps = append(ps, pl)
		open = open && pl.Worldwide
		known = known && pl.Known()
		zoneCountries = append(zoneCountries, pl.Countries...)
	}
	u := map[string]float64{
		"role":               role(g.TargetRoles, v.Title),
		"money":              money(g.Compensation, v.Money, r.Rates),
		"location":           location(g.PreferredLocations, ps, open),
		"remote_restriction": restriction(p.Authorizations, ps, has(v.WorkModes, "remote"), open, known),
		"time_zone":          na,
		"work_mode":          na,
		"engagement":         overlap(g.Engagement, engagementDefault(v.Engagement)),
		"hours":              hours(g.Hours, v.Hours),
		"requirements":       na,
	}
	if len(g.TimeZones) > 0 {
		zones := places.ZoneBands(v.TimeZone)
		if zones == nil {
			zones = places.BandsOf(zoneCountries)
		}
		u["time_zone"] = overlap(g.TimeZones, zones)
	}
	if u["work_mode"] = overlap(g.WorkModes, v.WorkModes); u["work_mode"] == 1 && has(v.WorkModes, "remote") && !open && has(g.WorkModes, "remote") {
		u["work_mode"] = na
	}
	f := Fit{Bucket: "Not scored"}
	if v.RequirementsRead {
		idx := skills.NewIndex(p.Skills, p.AbsentSkills)
		var reqNum, reqDen float64
		for i, req := range v.Requirements {
			w := 1.0
			if req.Level == "required" {
				w = 2.0
			}
			dr := skills.DecideFull(req, idx, p.Confirmed, p.DerivedYears)
			switch dr.Verdict {
			case "✓":
				reqNum += w
				f.ReqOK++
			case "✕":
				f.ReqNo++
			default:
				reqNum += w * 0.5
				f.ReqMaybe++
			}
			reqDen += w
			f.Requirements = append(f.Requirements, RequirementLine{
				Index: i, Text: req.Text, Alts: req.Alts, Level: req.Level,
				Verdict: dr.Verdict, Evidence: dr.Evidence, NamedSkills: dr.NamedSkills,
			})
		}
		f.ReqRead = true
		if reqDen > 0 && (f.ReqOK > 0 || f.ReqNo > 0) {
			u["requirements"] = reqNum / reqDen
		}
	}
	var num, den float64
	for _, name := range Criteria {
		if w := r.Weights[name]; w > 0 {
			c := Criterion{name, w, max(u[name], 0.5), u[name] >= 0}
			if c.Known {
				c.Utility = u[name]
			}
			f.Criteria = append(f.Criteria, c)
			num, den = num+w*c.Utility, den+w
		}
	}
	switch {
	case !v.Full:
		f.State, f.Text = NoText, Texts[NoText]
	case den == 0 || len(g.TargetRoles)+len(g.Engagement)+len(g.WorkModes)+len(g.TimeZones)+len(g.PreferredLocations) == 0 &&
		(g.Hours == nil || g.Hours.Mode == "") && (g.Compensation == nil || g.Compensation.CashMin == nil || *g.Compensation.CashMin <= 0):
		f.State, f.Text = NoGoals, Texts[NoGoals]
	default:
		f.State, f.Score = Scored, int(math.Round(num/den*100))
		f.Bucket = Bucket(f.Score)
	}
	values, converted, lines := texts(p, v, open, r.Rates)
	for i, t := range tiles {
		f.Tiles = append(f.Tiles, Tile{t.name, values[i], converted[i], lines[i], verdict(f.Criteria, t.criteria)})
	}
	return f
}

func Bucket(score int) string {
	switch {
	case score >= StrongFrom:
		return "Strong"
	case score >= PartialFrom:
		return "Partial"
	}
	return "Weak"
}

func TileFor(ts []Tile, criterion string) (Tile, bool) {
	for i, td := range tiles {
		if slices.Contains(td.criteria, criterion) && i < len(ts) {
			return ts[i], true
		}
	}
	return Tile{}, false
}

func verdict(cs []Criterion, names []string) string {
	out := "?"
	for _, c := range cs {
		if c.Known && has(names, c.Name) {
			if c.Utility < 1 {
				return "✕"
			}
			out = "✓"
		}
	}
	return out
}

func role(targets []string, title string) float64 {
	best := na
	tt := tokens(title)
	for _, t := range targets {
		rt := tokens(t)
		if len(rt) == 0 || len(tt) == 0 {
			continue
		}
		common := 0
		for _, x := range rt {
			if has(tt, x) {
				common++
			}
		}
		best = max(best, 2*float64(common)/float64(len(rt)+len(tt)))
	}
	return best
}

func tokens(s string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+' && r != '#' && r != '.'
	}) {
		if t = strings.Trim(t, "."); t != "" && !has(out, t) {
			out = append(out, t)
		}
	}
	return out
}

func money(c *candidate.Compensation, m *reading.Money, rates map[string]float64) float64 {
	if c == nil || c.CashMin == nil || *c.CashMin <= 0 || m == nil || m.Max <= 0 || m.Currency == "" ||
		(m.Tax == "net") != strings.EqualFold(c.GrossNet, "net") {
		return na
	}
	if hi := convert(m.Max, m, c.Currency, c.Period, rates); hi > 0 {
		return min(1, hi / *c.CashMin)
	}
	return na
}

func convert(x float64, m *reading.Money, cur, period string, rates map[string]float64) float64 {
	if cur != "" && !strings.EqualFold(cur, m.Currency) {
		from, to := rates[m.Currency], rates[strings.ToUpper(cur)]
		if from <= 0 || to <= 0 {
			return 0
		}
		x = x * from / to
	}
	return x * perYear(m.Period) / perYear(period)
}

func perYear(period string) float64 {
	if n := map[string]float64{"month": 12, "day": 260, "hour": 2080}[period]; n > 0 {
		return n
	}
	return 1
}

func location(prefs []candidate.Location, ps []places.Place, open bool) float64 {
	if len(prefs) == 0 {
		return na
	}
	if open {
		return 1
	}
	decided := len(ps) > 0
	for _, pref := range prefs {
		cs := places.Codes(pref.Country)
		decided = decided && cs != nil
		for _, pl := range ps {
			decided = decided && pl.Known() && !pl.Worldwide
			sameCity := pref.City == "" || len(pl.Cities) == 0 || has(pl.Cities, pref.City)
			if (cs != nil && overlap(cs, pl.Countries) == 1 && sameCity) || (cs == nil && pref.City != "" && has(pl.Cities, pref.City)) {
				return 1
			}
		}
	}
	if decided {
		return 0
	}
	return na
}

func restriction(auths []candidate.WorkAuthorization, ps []places.Place, remote, open, known bool) float64 {
	switch {
	case !remote:
		return na
	case open:
		return 1
	case !known:
		return na
	}
	var restricted []string
	for _, pl := range ps {
		restricted = append(restricted, pl.Countries...)
	}
	out := na
	for _, a := range auths {
		if overlap(places.Codes(a.Country), restricted) == 1 {
			if a.Status == "eligible" {
				return 1
			}
			out = 0
		}
	}
	return out
}

func hours(h *candidate.Hours, vh *reading.Hours) float64 {
	if h == nil || h.Mode == "" || vh == nil || vh.Mode == "" {
		return na
	}
	if h.Mode != vh.Mode {
		return 0
	}
	lo, hi := h.WeeklyMin, h.WeeklyMax
	if h.Weekly != nil {
		lo, hi = h.Weekly, h.Weekly
	}
	switch {
	case h.Mode != "part_time" || lo == nil && hi == nil:
		return 1
	case vh.Min == 0 && vh.Max == 0:
		return na
	case hi != nil && vh.Min > *hi || lo != nil && vh.Max > 0 && *lo > vh.Max:
		return 0
	}
	return 1
}

func engagementDefault(e string) []string {
	if e == "" {
		return []string{"employment"}
	}
	return strings.Fields(e)
}

func overlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return na
	}
	for _, x := range a {
		if has(b, x) {
			return 1
		}
	}
	return 0
}

func has(xs []string, x string) bool {
	for _, y := range xs {
		if strings.EqualFold(strings.TrimSpace(y), strings.TrimSpace(x)) {
			return true
		}
	}
	return false
}

func texts(p ProfileView, v reading.VacancyReading, open bool, rates map[string]float64) ([]string, []string, []string) {
	g := p.Goals
	val := []string{"Not stated", "Not stated", list(v.WorkModes, "Not stated"), "Not stated"}
	conv := []string{"", "", "", ""}
	line := []string{"No minimum stated", "No preference stated", "You want: " + list(g.WorkModes, "not stated"), "You want: not stated"}
	if m := v.Money; m != nil {
		val[0] = symbol(m.Currency) + cash(m.Min, m.Max) + per(m.Period)
		if eur := convert(m.Max, m, "EUR", "year", rates); eur > 0 {
			conv[0] = "≈ €" + amount(rounded(eur))
		}
	}
	if c := g.Compensation; c != nil && c.CashMin != nil {
		line[0] = "Your minimum: " + strings.TrimSpace(c.Currency+" "+amount(*c.CashMin)+per(c.Period)+" "+c.GrossNet)
	}
	if len(v.Locations) > 0 {
		val[1] = strings.Join(v.Locations, " · ")
	} else if open {
		val[1] = "Remote, anywhere"
	}
	var want []string
	for _, l := range g.PreferredLocations {
		want = append(want, strings.Trim(l.City+", "+l.Country, ", "))
	}
	for _, b := range places.Bands() {
		if has(g.TimeZones, b.Code) {
			want = append(want, b.Label)
		}
	}
	if len(want) > 0 {
		line[1] = "You prefer: " + strings.Join(want, " · ")
	}
	engLabel := v.Engagement
	if engLabel == "" {
		engLabel = "Employment (assumed)"
	}
	contract := []string{engLabel}
	if h := v.Hours; h != nil {
		contract = append(contract, h.Mode, strings.Trim(amount(h.Min)+"–"+amount(h.Max), "–")+map[bool]string{true: " h/week"}[h.Max > 0])
	}
	goal := append([]string{}, g.Engagement...)
	if h := g.Hours; h != nil {
		goal = append(goal, h.Mode)
	}
	val[3], line[3] = list(contract, "Not stated"), "You want: "+list(goal, "not stated")
	return val, conv, line
}

func symbol(currency string) string {
	for _, c := range config.Currencies {
		if c.Symbol != "" && strings.EqualFold(c.Code, currency) {
			return c.Symbol
		}
	}
	if currency == "" {
		return ""
	}
	return currency + " "
}

func cash(low, high float64) string {
	lo, loUnit := compact(low)
	hi, hiUnit := compact(high)
	switch {
	case lo == "":
		return hi + hiUnit
	case hi == "":
		return lo + loUnit
	case loUnit == hiUnit:
		return lo + "–" + hi + hiUnit
	}
	return lo + loUnit + "–" + hi + hiUnit
}

func compact(x float64) (string, string) {
	for _, u := range []struct {
		size float64
		unit string
	}{{1e6, "M"}, {1e3, "k"}} {
		if x >= u.size {
			return strconv.FormatFloat(math.Round(x/u.size*10)/10, 'f', -1, 64), u.unit
		}
	}
	return amount(x), ""
}

func list(xs []string, empty string) string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(strings.ReplaceAll(x, "_", "-")); x != "" {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return empty
	}
	return strings.Join(out, " · ")
}

func per(period string) string {
	if period == "" {
		return ""
	}
	if period == "hour" {
		return " an " + period
	}
	return " a " + period
}

func rounded(x float64) float64 {
	if x < 1000 {
		return x
	}
	return math.Round(x/1000) * 1000
}

func amount(x float64) string {
	if x <= 0 {
		return ""
	}
	s := strconv.FormatInt(int64(math.Round(x)), 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
