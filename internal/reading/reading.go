package reading

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/config"
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

type Money struct {
	Min, Max float64
	Currency string
	Period   string
	Tax      string
}

type Hours struct {
	Mode     string
	Min, Max float64
}

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

//go:embed labels.yaml
var wordsYAML []byte

var (
	labels, modes       = loadWords()
	amountRe            = regexp.MustCompile(`(\d{1,3}(?:[,\s]\d{3})+|\d+(?:\.\d+)?)\s*([kK])?`)
	currencyRe, symbols = currencyPattern()
	notCashRe           = regexp.MustCompile(`(?i)\b(bonus|equity|stock[- ]option|commission|estimate|survey|ote|allowance|relocation)\b`)
	weeklyRe            = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)(?:\s*(?:-|–|—|to)\s*(\d+(?:\.\d+)?))?\s*(?:h|hr|hrs|hours)\s*(?:/|per)?\s*week`)
	internRe            = regexp.MustCompile(`\bintern(ship)?\b`)
)

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
		r.Money = money(v, v)
	}
	if len(r.WorkModes) == 0 && rec.WorkMode != nil && rec.WorkMode.Value != "" {
		if m, ok := workMode(rec.WorkMode.Value); ok {
			r.addMode(m)
		}
	}
	if r.Engagement == "" && rec.Engagement != nil && rec.Engagement.Value != "" {
		r.Engagement = engagement(strings.ToLower(rec.Engagement.Value))
	}
	if len(r.Locations) == 0 && rec.Location != nil && rec.Location.Value != "" {
		r.addPlaces(rec.Location.Value)
	}
	if r.TimeZone == "" && rec.TimeZone != nil && rec.TimeZone.Value != "" {
		r.TimeZone = rec.TimeZone.Value
	}
	if r.Hours == nil && rec.Hours != nil && rec.Hours.Value != "" {
		r.setHours(rec.Hours.Value)
	}
	r.Requirements = rec.Requirements
	r.RequirementsRead = true
	return r
}

func FromCode(v Vacancy) VacancyReading {
	r := VacancyReading{Title: v.Title, Full: v.Full}
	if s := v.Fields["salary"]; s != "" {
		r.Money = money(s, s)
	}
	if t := strings.ReplaceAll(strings.ToLower(v.Fields["employment_type"]), "_", " "); t != "" {
		r.Engagement = engagement(t)
		r.setHours(t)
	}
	switch strings.ToLower(strings.TrimSpace(v.Fields["workplace_type"])) {
	case "onsite_or_remote":
		r.addMode("remote")
		r.addMode("onsite")
	default:
		if m, ok := workMode(v.Fields["workplace_type"]); ok {
			r.addMode(m)
		}
	}
	r.addPlaces(v.Location)
	for _, line := range strings.Split(v.Text, "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		value := strings.TrimSpace(line[i+1:])
		switch labels[strings.ToLower(strings.Join(strings.Fields(line[:i]), " "))] {
		case "money":
			if r.Money == nil {
				r.Money = money(value, line)
			}
		case "location":
			r.addPlaces(value)
		case "mode":
			if m, ok := workMode(value); ok {
				r.addMode(m)
			}
		case "engagement":
			if r.Engagement == "" {
				r.Engagement = engagement(strings.ToLower(value))
			}
		case "hours":
			if r.Hours == nil {
				r.setHours(value)
			}
		case "zone":
			if r.TimeZone == "" {
				r.TimeZone = value
			}
		}
	}
	for _, t := range v.Tags {
		low := strings.ToLower(strings.TrimSpace(t))
		if m, ok := workMode(low); ok && len(r.WorkModes) == 0 {
			r.addMode(m)
		}
		if r.Hours == nil && (low == "full-time" || low == "part-time") {
			r.setHours(low)
		}
		if r.Engagement == "" && (low == "contract" || low == "freelance") {
			r.Engagement = "contract"
		}
	}
	return r
}

func (r *VacancyReading) addPlaces(value string) {
	for _, part := range strings.FieldsFunc(value, func(c rune) bool { return c == '·' || c == ';' || c == '|' }) {
		part = strings.TrimSpace(part)
		if m, ok := workMode(part); ok {
			r.addMode(m)
		} else if part != "" && !hasFold(r.Locations, part) {
			r.Locations = append(r.Locations, part)
		}
	}
}

func (r *VacancyReading) addMode(m string) {
	if !hasFold(r.WorkModes, m) {
		r.WorkModes = append(r.WorkModes, m)
	}
}

func (r *VacancyReading) setHours(value string) {
	low := strings.ToLower(value)
	h := Hours{}
	switch {
	case strings.Contains(low, "full-time") || strings.Contains(low, "full time") || strings.Contains(low, "fulltime"):
		h.Mode = "full_time"
	case strings.Contains(low, "part-time") || strings.Contains(low, "part time") || strings.Contains(low, "parttime"):
		h.Mode = "part_time"
	}
	if m := weeklyRe.FindStringSubmatch(value); m != nil {
		h.Max, _ = strconv.ParseFloat(m[1], 64)
		if m[2] != "" {
			h.Min = h.Max
			h.Max, _ = strconv.ParseFloat(m[2], 64)
		}
	}
	if h != (Hours{}) {
		r.Hours = &h
	}
}

func workMode(value string) (string, bool) {
	m, ok := modes[strings.ToLower(strings.TrimSpace(strings.Trim(value, ".,:;")))]
	return m, ok
}

func loadWords() (map[string]string, map[string]string) {
	var v struct{ Labels, Modes map[string][]string }
	if err := yaml.Unmarshal(wordsYAML, &v); err != nil {
		panic(fmt.Sprintf("reading: load labels.yaml: %v", err))
	}
	flip := func(in map[string][]string) map[string]string {
		out := map[string]string{}
		for k, ws := range in {
			for _, w := range ws {
				out[w] = k
			}
		}
		return out
	}
	return flip(v.Labels), flip(v.Modes)
}

func engagement(low string) string {
	switch {
	case strings.Contains(low, "contract") || strings.Contains(low, "freelance") ||
		internRe.MatchString(low) || strings.Contains(low, "temporar"):
		return "contract"
	case strings.Contains(low, "employment") || strings.Contains(low, "permanent") ||
		strings.Contains(low, "full-time") || strings.Contains(low, "full time") ||
		strings.Contains(low, "fulltime") || strings.Contains(low, "employee") ||
		strings.Contains(low, "part-time") || strings.Contains(low, "part time") ||
		strings.Contains(low, "parttime"):
		return "employment"
	}
	return ""
}

func money(value, line string) *Money {
	low := strings.ToLower(line)
	salary := strings.Contains(low, "salary") || strings.Contains(low, " base ") || strings.Contains(low, "wage")
	if !salary && notCashRe.MatchString(low) {
		return nil
	}
	m := &Money{Currency: currency(value), Period: period(low)}
	if m.Currency == "" {
		m.Currency = currency(line)
	}
	if strings.Contains(low, "gross") {
		m.Tax = "gross"
	} else if strings.Contains(low, "net") {
		m.Tax = "net"
	}
	var amounts []float64
	var hasK []bool
	idx := amountRe.FindAllStringSubmatchIndex(value, -1)
	for _, x := range idx {
		n, err := strconv.ParseFloat(strings.NewReplacer(",", "", " ", "").Replace(value[x[2]:x[3]]), 64)
		if err != nil {
			return nil
		}
		k := x[4] >= 0
		hasK = append(hasK, k)
		if k {
			n *= 1000
		}
		amounts = append(amounts, n)
	}
	switch {
	case len(amounts) > 2:
		if len(amounts)%2 != 0 {
			return nil
		}
		m.Min, m.Max = amounts[0], amounts[0]
		for _, a := range amounts {
			m.Min, m.Max = min(m.Min, a), max(m.Max, a)
		}
		if m.Min < 1000 || m.Max > 10_000_000 {
			return nil
		}
	case len(amounts) == 2:
		between := strings.ToLower(strings.Trim(value[idx[0][1]:idx[1][0]], symbols+" \t,"))
		if between != "-" && between != "–" && between != "—" && between != "to" {
			return nil
		}
		if hasK[1] && !hasK[0] {
			amounts[0] *= 1000
		}
		m.Min, m.Max = min(amounts[0], amounts[1]), max(amounts[0], amounts[1])
	case len(amounts) == 1 && (hasAny(low, " from ", "starting at", "starting from", "minimum", "at least", ">=") || strings.HasPrefix(low, "from ")):
		m.Min = amounts[0]
	case len(amounts) == 1:
		m.Max = amounts[0]
	default:
		return nil
	}
	return m
}

func currency(s string) string {
	if c := currencyRe.FindString(s); c != "" {
		return strings.ToUpper(c)
	}
	for _, c := range config.Currencies {
		if c.Symbol != "" && strings.Contains(s, c.Symbol) {
			return c.Code
		}
	}
	return ""
}

func currencyPattern() (re *regexp.Regexp, symbols string) {
	var codes []string
	for _, c := range config.Currencies {
		codes, symbols = append(codes, c.Code), symbols+c.Symbol
	}
	return regexp.MustCompile(`(?i)\b(` + strings.Join(codes, "|") + `)\b`), symbols
}

func period(low string) string {
	switch {
	case hasAny(low, "per year", "/year", "yearly", "annual", "p.a.", "per-year-salary", "1 year"):
		return "year"
	case hasAny(low, "per month", "/month", "monthly", "1 month"):
		return "month"
	case hasAny(low, "per day", "/day", "daily"):
		return "day"
	case hasAny(low, "per hour", "/hour", "hourly", "1 hour"):
		return "hour"
	}
	return ""
}

func hasAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func hasFold(xs []string, x string) bool {
	for _, y := range xs {
		if strings.EqualFold(strings.Join(strings.Fields(y), " "), strings.Join(strings.Fields(x), " ")) {
			return true
		}
	}
	return false
}
