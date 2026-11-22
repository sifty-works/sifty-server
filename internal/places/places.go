package places

import (
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed places.yaml
var vocabYAML []byte

type ReferenceCountry struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type ReferenceRegion struct {
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	Size      int      `json:"size"`
	Aliases   []string `json:"aliases"`
	Countries []string `json:"countries"`
}

type ReferenceTimeZoneBand struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type vocabulary struct {
	Countries, Territories []struct {
		Code, Name     string
		Aliases, Bands []string
	}
	Regions []struct {
		Code, Name         string
		Aliases, Countries []string
	}
	Worldwide, Ignore, Prefixes, Qualifiers, Separators []string
	Bands                                               []struct {
		Code, Label string
		Min, Max    float64
	}
	Abbreviations map[string]float64
}

var (
	vocab     = mustLoad()
	index     = map[string][]string{}
	ignored   = map[string]bool{}
	bandsOf   = map[string][]string{}
	splitRe   *regexp.Regexp
	abbrevRe  *regexp.Regexp
	offsetRe  = regexp.MustCompile(`(?i)(?:UTC|GMT)\s*([+\-])\s*(\d{1,2})(?::(\d{2}))?`)
	widenRe   = regexp.MustCompile(`(?i)(?:±|[+\-]/?[+\-]?)\s*(\d+(?:\.\d+)?)\s*h(?:ours?)?`)
	countries []ReferenceCountry
	regions   []ReferenceRegion
	bands     []ReferenceTimeZoneBand
)

func mustLoad() vocabulary {
	var v vocabulary
	if err := yaml.Unmarshal(vocabYAML, &v); err != nil {
		panic(fmt.Sprintf("places: load places.yaml: %v", err))
	}
	return v
}

func init() {
	add := func(key string, cs []string) {
		if k := strings.ToLower(strings.TrimSpace(key)); k != "" && index[k] == nil {
			index[k] = cs
		}
	}
	for _, w := range vocab.Worldwide {
		add(w, []string{})
	}
	for _, r := range vocab.Regions {
		for _, k := range append([]string{r.Code, r.Name}, r.Aliases...) {
			add(k, r.Countries)
		}
		regions = append(regions, ReferenceRegion{r.Code, r.Name, len(r.Countries), r.Aliases, r.Countries})
	}
	for i, c := range append(vocab.Countries, vocab.Territories...) {
		for _, k := range append([]string{c.Code, c.Name}, c.Aliases...) {
			add(k, []string{c.Code})
		}
		bandsOf[c.Code] = c.Bands
		if i < len(vocab.Countries) {
			countries = append(countries, ReferenceCountry{c.Code, c.Name})
		}
	}
	for _, w := range vocab.Ignore {
		ignored[w] = true
	}
	var seps, abbrevs []string
	for _, s := range vocab.Separators {
		if strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz") {
			s = `\b` + regexp.QuoteMeta(s) + `\b`
		} else {
			s = regexp.QuoteMeta(s)
		}
		seps = append(seps, s)
	}
	splitRe = regexp.MustCompile(`(?i)` + strings.Join(seps, "|"))
	for k := range vocab.Abbreviations {
		abbrevs = append(abbrevs, k)
	}
	sort.Slice(abbrevs, func(i, j int) bool { return len(abbrevs[i]) > len(abbrevs[j]) })
	abbrevRe = regexp.MustCompile(`(?i)\b(` + strings.Join(abbrevs, "|") + `)\b`)
	for _, b := range vocab.Bands {
		bands = append(bands, ReferenceTimeZoneBand{b.Code, b.Label})
	}
}

func Countries() []ReferenceCountry { return countries }

func Regions() []ReferenceRegion { return regions }

func Bands() []ReferenceTimeZoneBand { return bands }

type Place struct {
	Worldwide bool
	Countries []string
	Cities    []string
}

func (p Place) Known() bool { return p.Worldwide || len(p.Countries) > 0 }

func Resolve(label string) Place {
	if cs, ok := lookup(label); ok {
		return Place{Worldwide: len(cs) == 0, Countries: cs}
	}
	var p Place
	seen := map[string]bool{}
	for _, part := range splitLabel(label) {
		low := strings.ToLower(part)
		if seen[low] || ignored[low] {
			continue
		}
		seen[low] = true
		cs, ok := lookup(part)
		switch {
		case !ok:
			p.Cities = append(p.Cities, part)
		case len(cs) == 0:
			p.Worldwide = true
		default:
			for _, c := range cs {
				if !slices.Contains(p.Countries, c) {
					p.Countries = append(p.Countries, c)
				}
			}
		}
	}
	if len(p.Countries) > 0 || len(p.Cities) > 0 {
		p.Worldwide = false
	}
	return p
}

func Codes(name string) []string {
	cs, _ := lookup(name)
	return cs
}

func BandsOf(cs []string) []string {
	has := map[string]bool{}
	for _, c := range cs {
		for _, b := range bandsOf[strings.ToUpper(c)] {
			has[b] = true
		}
	}
	var out []string
	for _, b := range vocab.Bands {
		if has[b.Code] {
			out = append(out, b.Code)
		}
	}
	return out
}

func ZoneBands(text string) []string {
	widen := 0.0
	if m := widenRe.FindStringSubmatch(text); m != nil {
		widen, _ = strconv.ParseFloat(m[1], 64)
		text = widenRe.ReplaceAllString(text, " ")
	}
	var off float64
	if m := offsetRe.FindStringSubmatch(text); m != nil {
		h, _ := strconv.ParseFloat(m[2], 64)
		mins, _ := strconv.ParseFloat("0"+m[3], 64)
		off = h + mins/60
		if m[1] == "-" {
			off = -off
		}
	} else if m := abbrevRe.FindString(text); m != "" {
		off = vocab.Abbreviations[strings.ToUpper(m)]
	} else {
		return nil
	}
	var out []string
	for _, b := range vocab.Bands {
		if off-widen <= b.Max && off+widen >= b.Min {
			out = append(out, b.Code)
		}
	}
	return out
}

func lookup(part string) ([]string, bool) {
	s := strings.TrimSpace(part)
	var forms []string
	for _, f := range append([]string{s}, stripPrefixes(s)...) {
		forms = append(forms, f)
		for _, q := range vocab.Qualifiers {
			if len(f) > len(q) && strings.EqualFold(f[len(f)-len(q):], q) {
				forms = append(forms, f[:len(f)-len(q)])
			}
		}
	}
	for _, f := range forms {
		if cs, ok := index[strings.ToLower(strings.TrimSpace(f))]; ok {
			return cs, true
		}
	}
	return nil, false
}

func stripPrefixes(s string) []string {
	var out []string
	for _, p := range vocab.Prefixes {
		if len(s) > len(p) && strings.EqualFold(s[:len(p)], p) && (s[len(p)] == ' ' || s[len(p)] == '\t') {
			out = append(out, strings.TrimSpace(s[len(p):]))
		}
	}
	return out
}

func splitLabel(label string) []string {
	var groups []string
	var outside strings.Builder
	depth, start := 0, 0
	for i, r := range label {
		switch {
		case r == '(':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case r == ')' && depth > 0:
			if depth--; depth == 0 {
				groups = append(groups, label[start:i])
			}
		case depth == 0:
			outside.WriteRune(r)
		}
	}
	var parts []string
	for _, g := range append(groups, outside.String()) {
		for _, p := range splitRe.Split(g, -1) {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
	}
	return parts
}

const Separator = " · "

func Short(locations []string) (shown, full string) {
	joined := strings.Join(locations, Separator)
	if len(locations) <= 3 {
		return joined, ""
	}
	return strings.Join(locations[:2], Separator) + fmt.Sprintf(" +%d more", len(locations)-2), joined
}
