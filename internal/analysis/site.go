package analysis

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/places"
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

type Span struct{ Start, End int }

type Site struct {
	Money      *Money
	Modes      []string
	Engagement string
	Hours      *Hours
	Places     []string
	TimeZone   string
	Spans      map[facts.Kind]Span
	Stated     map[string]Span
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

func ReadSite(doc *Doc) Site {
	s := Site{Spans: map[facts.Kind]Span{}, Stated: map[string]Span{}}
	if f := doc.Fields["salary"]; f != "" {
		s.Money = ParseMoney(f, f)
	}
	if t := strings.ReplaceAll(strings.ToLower(doc.Fields["employment_type"]), "_", " "); t != "" {
		s.Engagement = ParseEngagement(t)
		s.Hours = ParseHours(t)
	}
	switch strings.ToLower(strings.TrimSpace(doc.Fields["workplace_type"])) {
	case "onsite_or_remote":
		s.AddMode("remote")
		s.AddMode("onsite")
	default:
		if m, ok := ParseMode(doc.Fields["workplace_type"]); ok {
			s.AddMode(m)
		}
	}
	s.AddPlaces(doc.Location)
	for _, line := range textLines(doc.Original) {
		i, width := strings.Index(line.Text, ":"), 1
		if j := strings.Index(line.Text, " - "); j >= 0 && (i < 0 || j < i) {
			i, width = j, 3
		}
		if i < 0 {
			continue
		}
		value := strings.TrimSpace(line.Text[i+width:])
		span := Span{line.Start, line.End}
		switch labels[strings.ToLower(strings.Join(strings.Fields(line.Text[:i]), " "))] {
		case "money":
			if s.Money == nil {
				if s.Money = ParseMoney(value, line.Text); s.Money != nil {
					s.Spans[facts.Money] = span
				}
			}
		case "location":
			var parts []string
			if items := placeItems(value); !places.Recognised(value) && len(items) > 0 {
				for _, it := range items {
					parts = append(parts, it.label)
				}
				s.AddPlaces(strings.Join(parts, "·"))
			} else {
				parts = s.AddPlaces(value)
			}
			for _, p := range parts {
				s.Stated[strings.ToLower(p)] = span
			}
		case "mode":
			if m, ok := ParseMode(value); ok {
				s.AddMode(m)
				s.Spans[facts.WorkMode] = span
			}
		case "engagement":
			if s.Engagement == "" {
				if s.Engagement = ParseEngagement(strings.ToLower(value)); s.Engagement != "" {
					s.Spans[facts.Engagement] = span
				}
			}
		case "hours":
			if s.Hours == nil {
				if s.Hours = ParseHours(value); s.Hours != nil {
					s.Spans[facts.Hours] = span
				}
			}
		case "zone":
			if s.TimeZone == "" {
				s.TimeZone = value
				s.Spans[facts.TimeZone] = span
			}
		}
	}
	for _, t := range doc.Tags {
		low := strings.ToLower(strings.TrimSpace(t))
		if m, ok := ParseMode(low); ok && len(s.Modes) == 0 {
			s.AddMode(m)
		}
		if s.Hours == nil && (low == "full-time" || low == "part-time") {
			s.Hours = ParseHours(low)
		}
		if s.Engagement == "" && (low == "contract" || low == "freelance") {
			s.Engagement = "contract"
		}
	}
	return s
}

func (s *Site) AddPlaces(value string) []string {
	var parts []string
	for _, part := range strings.FieldsFunc(value, func(c rune) bool { return c == '·' || c == ';' || c == '|' }) {
		part = strings.TrimSpace(part)
		if trimmed := strings.TrimRight(part, "."); !strings.Contains(trimmed, ".") {
			part = trimmed
		}
		if m, ok := ParseMode(part); ok {
			s.AddMode(m)
		} else if part != "" {
			parts = append(parts, part)
			if !hasFold(s.Places, part) {
				s.Places = append(s.Places, part)
			}
		}
	}
	return parts
}

func (s *Site) AddMode(m string) {
	if !hasFold(s.Modes, m) {
		s.Modes = append(s.Modes, m)
	}
}

func ParseHours(value string) *Hours {
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
	if h == (Hours{}) {
		return nil
	}
	return &h
}

func ParseMode(value string) (string, bool) {
	m, ok := modes[strings.ToLower(strings.TrimSpace(strings.Trim(value, ".,:;")))]
	return m, ok
}

func loadWords() (map[string]string, map[string]string) {
	var v struct{ Labels, Modes map[string][]string }
	if err := yaml.Unmarshal(wordsYAML, &v); err != nil {
		panic(fmt.Sprintf("analysis: load labels.yaml: %v", err))
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

func ParseEngagement(low string) string {
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

func ParseMoney(value, line string) *Money {
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
