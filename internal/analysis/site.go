package analysis

import (
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

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
	aggregator bool
}

const weeklyPattern = `(\d+(?:\.\d+)?)(?:\s*(?:-|–|—|to)\s*(\d+(?:\.\d+)?))?\s*(?:h|hr|hrs|hours)\s*(?:/|per)?\s*week`

var internWordPattern = alt(vocab.Engagement["intern_words"])

//go:embed labels.yaml
var wordsYAML []byte

var (
	labels, modes                                       = loadWords()
	amountRe                                            = regexp.MustCompile(`(\d{1,3}(?:[,\s\x{00a0}\x{202f}]\d{3})+|\d+(?:\.\d+)?)\s*([kK])?`)
	currencyCodes, currencyMarkers, markerCode, symbols = loadCurrencies()
	currencyRe                                          = regexp.MustCompile(`(?i)\b(` + strings.Join(currencyCodes, "|") + `)\b`)
	weeklyRe                                            = regexp.MustCompile(`(?i)` + weeklyPattern)
	markerRe                                            = regexp.MustCompile(`(?i)` + alt(currencyMarkers))
	internTitleRe                                       = regexp.MustCompile(`(?i)\b(?:` + internWordPattern + `)s?\b\s*(?:$|[-–(,:|])`)
	internRe                                            = regexp.MustCompile(`(?i)\b(?:` + internWordPattern + `)s?\b`)
	flagRe                                              = regexp.MustCompile(`[\x{1F1E6}-\x{1F1FF}]{2}\s*`)
	trailingOnlyRe                                      = regexp.MustCompile(`(?i)\s+only$`)
	gluedLabelRe                                        = regexp.MustCompile(`[\p{Ll})\]](\p{Lu}[\p{L} ]*?):`)
)

func ReadSite(doc *Doc) Site {
	s := Site{Spans: map[facts.Kind]Span{}, Stated: map[string]Span{}, aggregator: doc.Origin == facts.Aggregator}
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
	s.addPlaceList(doc.Location)
	lines := textLines(doc.Original)
	for k, line := range lines {
		n := k + 1
		for n < len(lines) && strings.HasPrefix(lines[n].Text, "(") && strings.HasSuffix(lines[n].Text, ")") {
			n++
		}
		next := ""
		if n < len(lines) {
			next, _, _ = strings.Cut(lines[n].Text, ". ")
		}
		if cleaned := trailingOnlyRe.ReplaceAllString(strings.Trim(flagRe.ReplaceAllString(next, ", "), ", "), ""); labels[labelKey(line.Text)] == "location" && placeList(cleaned) {
			line = Line{Text: line.Text + ": " + cleaned, Start: line.Start, End: lines[n].Start + len(next)}
		}
		i, width := strings.Index(line.Text, ":"), 1
		if j := strings.Index(line.Text, " - "); j >= 0 && (i < 0 || j < i) {
			i, width = j, 3
		}
		if i < 0 {
			continue
		}
		value := strings.TrimSpace(line.Text[i+width:])
		span := Span{line.Start, line.End}
		key := labelKey(line.Text[:i])
		for _, m := range gluedLabelRe.FindAllStringSubmatchIndex(line.Text, -1) {
			if glued := labelKey(line.Text[m[2]:m[3]]); labels[key] == "" && labels[glued] != "" {
				key, value = glued, strings.TrimSpace(line.Text[m[3]+1:])
			}
		}
		switch labels[key] {
		case "money":
			if s.Money == nil {
				if s.Money = ParseMoney(value, line.Text); s.Money != nil {
					s.Spans[facts.Money] = span
				}
			}
		case "location":
			parts := s.addPlaceList(value)
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
	if s.Engagement == "" && internTitleRe.MatchString(doc.Title) {
		s.Engagement = "internship"
	}
	for _, t := range doc.Tags {
		low := strings.ToLower(strings.TrimSpace(t))
		if m, ok := ParseMode(low); ok && len(s.Modes) == 0 {
			s.AddMode(m)
		}
		if s.Hours == nil && (low == "full-time" || low == "part-time") {
			s.Hours = ParseHours(low)
		}
		if s.Engagement == "" && slices.Contains(vocab.Engagement["tags"], low) {
			s.Engagement = ParseEngagement(low)
		}
	}
	return s
}

func placeList(text string) bool {
	covered := 0
	for _, it := range placeItems(text) {
		covered += it.end - it.start
	}
	return covered > 0 && covered*2 >= len(text)
}

func labelKey(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func (s *Site) addPlaceList(value string) []string {
	value = strings.Trim(flagRe.ReplaceAllString(value, ", "), ", ")
	items := placeItems(value)
	if len(items) == 0 || strings.ContainsAny(value, "·;|") || len(items) == 1 && value == items[0].label {
		return s.AddPlaces(value)
	}
	var parts []string
	for _, it := range items {
		parts = append(parts, it.label)
	}
	for _, m := range modeWordRe.FindAllString(value, -1) {
		if mode, ok := ParseMode(m); ok {
			s.AddMode(mode)
		}
	}
	s.AddPlaces(strings.Join(parts, "·"))
	return parts
}

func (s *Site) AddPlaces(value string) []string {
	var parts []string
	for _, part := range strings.FieldsFunc(value, func(c rune) bool { return c == '·' || c == ';' || c == '|' }) {
		part = s.takeModes(strings.TrimSpace(part))
		if trimmed := strings.TrimRight(part, "."); !strings.Contains(trimmed, ".") {
			part = trimmed
		}
		if len(part) == 2 && part != strings.ToUpper(part) {
			continue
		}
		if name, ok := places.CountryName(part); ok {
			part = name
		}
		if m, ok := ParseMode(part); ok {
			s.AddMode(m)
		} else if part != "" && !notPlace(part) && !(s.aggregator && bareWorldwide(part)) {
			parts = append(parts, part)
			if !hasFold(s.Places, part) {
				s.Places = append(s.Places, part)
			}
		}
	}
	return parts
}

func (s *Site) takeModes(part string) string {
	var rest []string
	for _, token := range strings.Split(part, ",") {
		if m, ok := ParseMode(token); ok && strings.Contains(part, ",") {
			s.AddMode(m)
		} else {
			rest = append(rest, strings.TrimSpace(token))
		}
	}
	return strings.Join(rest, ", ")
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
	low = companyContractorRe.ReplaceAllString(low, " ")
	switch {
	case internRe.MatchString(low):
		return "internship"
	case employmentContractRe.MatchString(low):
		return "employment"
	case hasAny(low, vocab.Engagement["field_contract"]...):
		return "contract"
	case hasAny(low, vocab.Engagement["field_employment"]...):
		return "employment"
	}
	return ""
}

func notPlace(label string) bool {
	low := strings.ToLower(strings.TrimSpace(label))
	if zoneCueRe.MatchString(low) || slices.Contains(vocab.Place["not_places"], low) || !strings.ContainsFunc(low, unicode.IsLetter) {
		return true
	}
	_, country := places.CountryName(low)
	short := slices.ContainsFunc(vocab.Place["short_codes"], func(c string) bool { return strings.EqualFold(c, low) })
	return len([]rune(low)) <= 2 && !country && !short && !places.IsCity(label)
}

func bareWorldwide(label string) bool {
	return slices.Contains(vocab.Place["bare_worldwide"], strings.ToLower(strings.TrimSpace(label)))
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
		k := x[4] >= 0 && (x[1] == len(value) || !unicode.IsLetter([]rune(value[x[1]:])[0]))
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
		between := strings.ToLower(strings.Trim(markerRe.ReplaceAllString(currencyRe.ReplaceAllString(value[idx[0][1]:idx[1][0]], ""), ""), symbols+" \t,"))
		if between != "-" && between != "–" && between != "—" && between != "to" {
			return nil
		}
		if hasK[1] && !hasK[0] {
			amounts[0] *= 1000
		}
		if amounts[1] < amounts[0] {
			return nil
		}
		m.Min, m.Max = amounts[0], amounts[1]
	case len(amounts) == 1 && (hasAny(low, " from ", "starting at", "starting from", "minimum", "at least", ">=") || strings.HasPrefix(low, "from ")):
		m.Min = amounts[0]
	case len(amounts) == 1:
		m.Max = amounts[0]
	default:
		return nil
	}
	floor := vocab.FloorWithoutPeriod
	if f, ok := vocab.Floors[m.Period]; ok {
		floor = f
	}
	if max(m.Min, m.Max) < floor {
		return nil
	}
	return m
}

func currency(s string) string {
	if c := currencyRe.FindString(s); c != "" {
		return strings.ToUpper(c)
	}
	for _, mk := range currencyMarkers {
		if strings.Contains(s, mk) {
			return markerCode[mk]
		}
	}
	for _, c := range config.Currencies {
		if c.Symbol != "" && strings.Contains(s, c.Symbol) {
			return c.Code
		}
	}
	return ""
}

func loadCurrencies() (codes, markers []string, byMarker map[string]string, symbols string) {
	byMarker = map[string]string{}
	for _, c := range config.Currencies {
		codes, symbols = append(codes, c.Code), symbols+c.Symbol
	}
	for code, ms := range vocab.Currencies {
		codes = append(codes, code)
		for _, m := range ms {
			if m != code {
				markers, byMarker[m] = append(markers, m), code
			}
		}
	}
	sort.Strings(codes)
	sort.Strings(markers)
	return
}

func period(low string) string {
	switch {
	case hasAny(low, "per year", "/year", "/yr", "per yr", "a year", "yearly", "annual", "p.a.", "per-year-salary", "1 year"):
		return "year"
	case hasAny(low, "per month", "/month", "/mo ", "per mo ", "a month", "monthly", "1 month"):
		return "month"
	case hasAny(low, "per day", "/day", "a day", "daily"):
		return "day"
	case hasAny(low, "per hour", "/hour", "/hr", "per hr", "an hour", "hourly", "1 hour"):
		return "hour"
	}
	return ""
}

func hasAny(s string, subs ...string) bool {
	return slices.ContainsFunc(subs, func(sub string) bool { return strings.Contains(s, sub) })
}

func hasFold(xs []string, x string) bool {
	norm := strings.Join(strings.Fields(x), " ")
	return slices.ContainsFunc(xs, func(y string) bool {
		return strings.EqualFold(strings.Join(strings.Fields(y), " "), norm)
	})
}
