package fit

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/places"
)

type ProfileView struct {
	Goals          candidate.GoalsView
	Authorizations []candidate.WorkAuthorization
	Languages      []candidate.Language
	Relocation     string
	Rates          map[string]float64 `json:"-"`
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

const na = -1.0

const (
	Scored  = "scored"
	NoText  = "no_text"
	NoGoals = "no_goals"
	NoFacts = "no_facts"
)

var Texts = map[string]string{
	"":      "Not compared yet",
	NoText:  "Full job description is unavailable",
	NoGoals: "Your profile states no goals to compare",
	NoFacts: "Nothing the vacancy states can be compared with your goals",
}

const (
	StrongFrom  = 75
	PartialFrom = 55

	CapBroken = 15
	CapWeak   = 40
)

type Criterion struct {
	Name       string
	Weight     float64
	Strictness Strictness
	Verdict
}

type Tile struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Converted string `json:"converted,omitempty"`
	Full      string `json:"full,omitempty"`
	Line      string `json:"line"`
	Verdict   string `json:"verdict"`
}

type Fit struct {
	State                  string
	Score                  int
	Bucket                 string
	Text                   string
	Criteria               []Criterion
	ScoredOn, Of           int
	Reasons                []string
	Broken                 bool
	Tiles                  []Tile
	ReqRead                bool
	ReqOK, ReqNo, ReqMaybe int
	Requirements           []RequirementLine
}

var tiles = []struct {
	name     string
	criteria []string
	empty    string
	value    func(v Vacancy, g geo, rates map[string]float64) (value, converted, full string)
}{
	{"Money", []string{"money"}, "No minimum stated", moneyTile},
	{"Location", []string{"location", "remote_restriction", "time_zone"}, "No preference stated", locationTile},
	{"Work mode", []string{"work_mode"}, "You want: not stated", func(v Vacancy, _ geo, _ map[string]float64) (string, string, string) {
		return list(v.WorkModes, "Not stated"), "", ""
	}},
	{"Contract", []string{"engagement", "hours"}, "You want: not stated", contractTile},
}

func Score(p ProfileView, v Vacancy, r Rules) Fit {
	f := Fit{Bucket: "Not scored", Reasons: []string{}}
	p.Rates = r.Rates
	if v.Analysed {
		f.ReqRead = true
		f.Requirements = requirementLines(p, v)
		v.Decided = f.Requirements
		for _, l := range f.Requirements {
			switch l.Verdict {
			case "✓":
				f.ReqOK++
			case "✕":
				f.ReqNo++
			default:
				f.ReqMaybe++
			}
		}
	}
	var num, den float64
	var text, weak, other, unknown []string
	capAt := 100
	for _, c := range Comparers {
		s := strictnessOf(c.Name())
		cr := Criterion{Name: c.Name(), Weight: r.Weights[c.Name()], Strictness: s, Verdict: c.Compare(v, p, s)}
		f.Criteria = append(f.Criteria, cr)
		switch {
		case cr.Outcome == Skipped || s == Ignore:
			continue
		case cr.Outcome == Unknown:
			unknown = append(unknown, cr.Reason)
		case cr.Outcome == Mismatch && s == Must && !cr.Soft:
			if weakSource(cr.Source) {
				capAt, weak = min(capAt, CapWeak), append(weak, cr.Reason)
			} else {
				capAt, f.Broken, text = min(capAt, CapBroken), true, append(text, cr.Reason)
			}
		case cr.Outcome == Mismatch:
			other = append(other, cr.Reason)
		}
		if cr.Weight > 0 {
			f.Of++
			if cr.Outcome != Unknown {
				f.ScoredOn++
				num, den = num+cr.Weight*cr.Utility, den+cr.Weight
			}
		}
	}
	for _, rs := range [][]string{text, weak, other, unknown} {
		for _, reason := range rs {
			if reason != "" {
				f.Reasons = append(f.Reasons, reason)
			}
		}
	}
	g := geoOf(v)
	for _, t := range tiles {
		value, converted, full := t.value(v, g, r.Rates)
		line, verdict := tileLine(f.Criteria, t.criteria, t.empty)
		f.Tiles = append(f.Tiles, Tile{t.name, value, converted, full, line, verdict})
	}
	switch {
	case !v.Full:
		f.Broken = false
		f.State, f.Text = NoText, Texts[NoText]
	case noGoals(p.Goals):
		f.Broken = false
		f.State, f.Text = NoGoals, Texts[NoGoals]
	case den == 0 && !f.Broken:
		f.State, f.Text = NoFacts, Texts[NoFacts]
	default:
		f.State = Scored
		if den > 0 {
			f.Score = min(capAt, int(math.Round(num/den*100)))
		}
		f.Bucket = Bucket(f.Score)
	}
	return f
}

func noGoals(g candidate.GoalsView) bool {
	return len(g.TargetRoles)+len(g.Engagement)+len(g.WorkModes)+len(g.TimeZones)+len(g.PreferredLocations) == 0 &&
		(g.Hours == nil || g.Hours.Mode == "") && (g.Compensation == nil || g.Compensation.CashMin == nil || *g.Compensation.CashMin <= 0)
}

func tileLine(cs []Criterion, names []string, empty string) (string, string) {
	var lines []string
	verdict, compared := "?", false
	for _, c := range cs {
		if !slices.Contains(names, c.Name) || c.Outcome == Skipped {
			continue
		}
		compared = true
		if c.Reason != "" {
			lines = append(lines, c.Reason)
		}
		switch {
		case c.Outcome == Mismatch:
			verdict = "✕"
		case c.Outcome == Match && verdict == "?":
			verdict = "✓"
		}
	}
	switch {
	case len(lines) > 0:
		return strings.Join(lines, " · "), verdict
	case compared:
		return "Not stated in the vacancy", verdict
	}
	return empty, verdict
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

func weakSource(s facts.Source) bool {
	return s.Way == "" || s.Way == facts.SiteField && s.Origin == facts.Aggregator
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

func money(c *candidate.Compensation, m *Money, rates map[string]float64) float64 {
	if c == nil || c.CashMin == nil || *c.CashMin <= 0 || m == nil || m.Max <= 0 || m.Currency == "" ||
		(m.Tax == "net") != strings.EqualFold(c.GrossNet, "net") {
		return na
	}
	if hi := convert(m.Max, m, c.Currency, c.Period, rates); hi > 0 {
		return min(1, hi / *c.CashMin)
	}
	return na
}

func convert(x float64, m *Money, cur, period string, rates map[string]float64) float64 {
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

func hours(h *candidate.Hours, vh *Hours) float64 {
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
	if math.Round(x) < 1000 {
		return amount(x), ""
	}
	if v := tenths(x / 1e3); v < 1000 {
		return strconv.FormatFloat(v, 'f', -1, 64), "k"
	}
	return strconv.FormatFloat(tenths(x/1e6), 'f', -1, 64), "M"
}

func tenths(x float64) float64 {
	return math.Round(x*10) / 10
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
