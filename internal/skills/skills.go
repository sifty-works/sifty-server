package skills

import (
	_ "embed"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

//go:embed vocab.yaml
var VocabBytes []byte

type vocabData struct {
	TechnologyContext []string `yaml:"technology_context"`
	OtherNames        []string `yaml:"other_names"`
	CompanyWords      []string `yaml:"company_words"`
	Numbers           map[string]int
	Levels            levelsData  `yaml:"levels"`
	Groups            []groupData `yaml:"groups"`
}

type levelsData struct {
	Order []string `yaml:"order"`
	Years []struct {
		From  int    `yaml:"from"`
		Level string `yaml:"level"`
	} `yaml:"years"`
	Words map[string][]string `yaml:"words"`
}

type groupData struct {
	Name           string   `yaml:"name"`
	Terms          []string `yaml:"terms"`
	AmbiguousTerms []string `yaml:"ambiguous_terms"`
	ExactTerms     []string `yaml:"exact_terms"`
}

type termInfo struct {
	group     string
	ambiguous bool
	noContext bool
	exact     string
}

type vocab struct {
	terms    map[string]termInfo
	maxWords int
	context  map[string]bool
	company  map[string]bool
	order    []string
	numbers  map[string]int
	years    []yearRule
	words    []wordRule
}

type yearRule struct {
	from  int
	level string
}

type wordRule struct {
	phrase string
	words  int
	level  string
}

var defaultVocab = func() *vocab {
	var d vocabData
	if err := yaml.Unmarshal(VocabBytes, &d); err != nil {
		panic("skills: load vocab.yaml: " + err.Error())
	}
	v := &vocab{terms: map[string]termInfo{}, context: map[string]bool{}, company: map[string]bool{}, order: d.Levels.Order, numbers: d.Numbers, maxWords: 1}
	for _, w := range d.TechnologyContext {
		v.context[strings.ToLower(strings.TrimSpace(w))] = true
	}
	for _, w := range d.CompanyWords {
		v.company[w] = true
	}
	for _, y := range d.Levels.Years {
		v.years = append(v.years, yearRule{y.From, y.Level})
	}
	for level, words := range d.Levels.Words {
		for _, w := range words {
			toks := tokenize(w)
			v.words = append(v.words, wordRule{joinLower(toks), len(toks), level})
			for _, t := range toks {
				v.context[t.lower] = true
			}
		}
	}
	add := func(group, term string, ambiguous, noContext bool) {
		toks := tokenize(term)
		if len(toks) == 0 {
			return
		}
		key := joinLower(toks)
		if len(toks) > v.maxWords {
			v.maxWords = len(toks)
		}
		v.terms[key] = termInfo{group, ambiguous, noContext, strings.TrimSpace(term)}
	}
	for _, t := range d.OtherNames {
		add("", t, false, false)
	}
	for _, g := range d.Groups {
		special := map[string]bool{}
		for _, t := range slices.Concat(g.AmbiguousTerms, g.ExactTerms) {
			special[strings.ToLower(strings.TrimSpace(t))] = true
		}
		if !special[strings.ToLower(strings.TrimSpace(g.Name))] {
			add(g.Name, g.Name, false, false)
		}
		for _, t := range g.Terms {
			add(g.Name, t, false, false)
		}
		for _, t := range g.AmbiguousTerms {
			add(g.Name, t, true, false)
		}
		for _, t := range g.ExactTerms {
			add(g.Name, t, true, true)
		}
	}
	return v
}()

type token struct {
	text, lower string
	start, end  int
}

func isWordRune(r rune) bool  { return unicode.IsLetter(r) || unicode.IsDigit(r) }
func firstRune(s string) rune { r, _ := utf8.DecodeRuneInString(s); return r }

func gluedSentence(prev rune, rest string) bool {
	head := []rune(rest[:min(len(rest), 12)])
	return unicode.IsLower(prev) && len(head) > 2 && unicode.IsUpper(head[0]) && unicode.IsLower(head[1]) && unicode.IsLower(head[2])
}

func tokenize(s string) []token {
	var out []token
	i := 0
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		pr, _ := utf8.DecodeLastRuneInString(s[:i])
		if !isWordRune(r) && !(r == '.' && i+sz < len(s) && isWordRune(firstRune(s[i+sz:])) && !isWordRune(pr)) {
			i += sz
			continue
		}
		start, prev, j := i, r, i+sz
		for j < len(s) {
			c, csz := utf8.DecodeRuneInString(s[j:])
			if isWordRune(c) ||
				((c == '+' || c == '#') && (isWordRune(prev) || prev == '+')) ||
				((c == '.' || c == '&') && j+csz < len(s) && isWordRune(firstRune(s[j+csz:])) && isWordRune(prev) && !(c == '.' && gluedSentence(prev, s[j+csz:]))) {
				prev, j = c, j+csz
				continue
			}
			break
		}
		text := s[start:j]
		out = append(out, token{text: text, lower: strings.ToLower(text), start: start, end: j})
		i = j
	}
	return out
}

type hit struct {
	group    string
	from, to int
}

func joinLower(toks []token) string {
	parts := make([]string, len(toks))
	for i, t := range toks {
		parts[i] = t.lower
	}
	return strings.Join(parts, " ")
}

func (v *vocab) scan(text string) (hits []hit, toks []token) {
	toks = tokenize(text)
	covered := make([]bool, len(toks))
	var cands []hit
	for i := 0; i < len(toks); i++ {
		for n := v.maxWords; n >= 1; n-- {
			if i+n > len(toks) {
				continue
			}
			info, ok := v.terms[joinLower(toks[i:i+n])]
			if !ok {
				continue
			}
			if info.group == "" || i > 0 && toks[i-1].lower == "non" || i+n < len(toks) && v.company[toks[i+n].text] {
				i += n - 1
				break
			}
			h := hit{info.group, i, i + n}
			if info.ambiguous {
				cands = append(cands, h)
			} else {
				hits = append(hits, h)
				for k := i; k < i+n; k++ {
					covered[k] = true
				}
			}
			i += n - 1
			break
		}
	}
	whole := len(toks) == 1
	for _, h := range cands {
		t := toks[h.from]
		info := v.terms[t.lower]
		ok := t.text == info.exact && (info.noContext || !joinedByHyphen(text, t.start, t.end))
		accepted := ok && (whole || info.noContext)
		if ok && !accepted {
			for k := h.from - 3; k <= h.from+3; k++ {
				if k < 0 || k >= len(toks) || k == h.from {
					continue
				}
				if v.context[toks[k].lower] || (covered[k] && k >= h.from-2 && k <= h.from+2) {
					accepted = true
					break
				}
			}
		}
		if accepted {
			hits = append(hits, h)
		}
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].from < hits[b].from })
	return hits, toks
}

func joinedByHyphen(text string, start, end int) bool {
	return (start > 0 && text[start-1] == '-') || (end < len(text) && text[end] == '-')
}

func (v *vocab) scanName(name string) []hit {
	toks := tokenize(strings.TrimSpace(name))
	if len(toks) == 0 {
		return nil
	}
	if info, ok := v.terms[joinLower(toks)]; ok && info.group != "" {
		return []hit{{info.group, 0, len(toks)}}
	}
	hits, _ := v.scan(name)
	return hits
}

func groupsOf(hits []hit) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hits {
		if !seen[h.group] {
			seen[h.group] = true
			out = append(out, h.group)
		}
	}
	return out
}

func Groups(name string) []string { return groupsOf(defaultVocab.scanName(name)) }

const (
	Any    = "Any"
	No     = "No"
	Listed = "Listed"
)

func (v *vocab) rank(level string) int {
	return slices.Index(v.order, level) + 1
}

type Held struct{ Name, Level string }

type Assumption struct{ Level, From string }

type Index struct {
	level   map[string]string
	absent  map[string]bool
	assumed map[string]Assumption
}

func NewIndex(held []Held, absent []string) *Index {
	idx := &Index{level: map[string]string{}, absent: map[string]bool{}}
	for _, h := range held {
		level := h.Level
		if defaultVocab.rank(level) == 0 {
			level = Listed
		}
		for _, g := range Groups(h.Name) {
			if defaultVocab.rank(level) >= defaultVocab.rank(idx.level[g]) {
				idx.level[g] = level
			}
		}
	}
	for _, name := range absent {
		for _, g := range Groups(name) {
			idx.absent[g] = true
		}
	}
	return idx
}

func (idx *Index) WithAssumed(assumed map[string]Assumption) *Index {
	idx.assumed = assumed
	return idx
}

func (idx *Index) Have(group string) string {
	switch {
	case idx.level[group] != "":
		return idx.level[group]
	case idx.absent[group]:
		return No
	}
	return idx.assumed[group].Level
}

func (idx *Index) Assumed(group string) (Assumption, bool) {
	if idx.level[group] != "" || idx.absent[group] {
		return Assumption{}, false
	}
	a, ok := idx.assumed[group]
	return a, ok
}

func (idx *Index) Verdict(e Entry) string {
	best, bestRank, unknown, allNo := "", 0, false, true
	for _, name := range e.Names {
		switch h := idx.Have(name); h {
		case "":
			unknown, allNo = true, false
		case No:
		default:
			allNo = false
			if best == "" || defaultVocab.rank(h) > bestRank {
				best, bestRank = h, defaultVocab.rank(h)
			}
		}
	}
	need, diff := defaultVocab.rank(e.Demand), 0
	switch {
	case best == "" && allNo:
		return "✕"
	case best == "":
		return "?"
	case need == 0:
		return "✓"
	case bestRank == 0:
		return "?"
	}
	diff = need - bestRank
	switch {
	case diff <= 0:
		return "✓"
	case unknown:
		return "?"
	case diff == 1:
		return "≈"
	}
	return "✕"
}

type Line struct {
	Text     string
	Required bool
}

type Entry struct {
	Names    []string
	Demand   string
	Required bool
	Line     string
	Index    int
}

var NumberPattern = func() string {
	var words []string
	for w := range defaultVocab.numbers {
		words = append(words, strings.ReplaceAll(w, "-", "[- ]"))
	}
	sort.Strings(words)
	return strings.Join(words, "|")
}()

var yearsRe = regexp.MustCompile(`(?i)\b(\d+|` + NumberPattern + `)(?:\s*(?:-|–|to)\s*\d+)?\s*\+?[\s-]*(?:years?|yrs?)\b`)

func NumberOf(word string) (int, bool) {
	if n, err := strconv.Atoi(word); err == nil {
		return n, true
	}
	n, ok := defaultVocab.numbers[strings.ReplaceAll(strings.ToLower(word), " ", "-")]
	return n, ok
}

func YearsIn(text string) (int, bool) {
	most := 0
	for _, m := range yearsRe.FindAllStringSubmatch(text, -1) {
		years, _ := NumberOf(m[1])
		most = max(most, years)
	}
	return most, most > 0
}

func (v *vocab) levelFor(years int) string {
	level := ""
	for i, y := range v.years {
		if i == 0 || years >= y.from {
			level = y.level
		}
	}
	return level
}

func LevelForYears(years int) string { return defaultVocab.levelFor(years) }

func Levels() []string { return slices.Clone(defaultVocab.order) }

func (v *vocab) demand(text string, toks []token, covered []bool) string {
	best := 0
	raise := func(level string) { best = max(best, v.rank(level)) }
	if years, ok := YearsIn(text); ok {
		raise(v.levelFor(years))
	}
	for _, w := range v.words {
		for i := 0; i+w.words <= len(toks); i++ {
			if joinLower(toks[i:i+w.words]) == w.phrase && !slices.Contains(covered[i:i+w.words], true) {
				raise(w.level)
			}
		}
	}
	if best == 0 {
		return Any
	}
	return v.order[best-1]
}

var alternativeGapRe = regexp.MustCompile(`^(?:,?or|and/or|/)(?:related|similar|other|equivalent|any|another)?$`)

func (v *vocab) chains(text string, hits []hit, toks []token) [][]string {
	var out [][]string
	var chain []string
	seenOr, hasOr := false, false
	last := -1
	flush := func() {
		if last >= 0 && last+1 < len(toks) && toks[last+1].lower == "or" {
			hasOr = true
		}
		switch {
		case len(chain) == 0:
		case hasOr:
			out = append(out, chain)
		default:
			for _, name := range chain {
				out = append(out, []string{name})
			}
		}
		chain, seenOr, hasOr = nil, false, false
	}
	for k, h := range hits {
		if k > 0 {
			gap := strings.ToLower(strings.Join(strings.Fields(text[toks[hits[k-1].to-1].end:toks[h.from].start]), ""))
			switch {
			case alternativeGapRe.MatchString(gap):
				seenOr, hasOr = true, true
			case gap == "," && !seenOr:
			default:
				flush()
			}
		}
		if !slices.Contains(chain, h.group) {
			chain = append(chain, h.group)
		}
		last = h.to - 1
	}
	flush()
	return out
}

func Entries(lines []Line) (entries []Entry, rest []int) {
	byKey := map[string]int{}
	for i, line := range lines {
		hits, toks := defaultVocab.scan(line.Text)
		if len(hits) == 0 {
			rest = append(rest, i)
			continue
		}
		covered := make([]bool, len(toks))
		for _, h := range hits {
			for k := h.from; k < h.to; k++ {
				covered[k] = true
			}
		}
		demand := defaultVocab.demand(line.Text, toks, covered)
		for _, names := range defaultVocab.chains(line.Text, hits, toks) {
			key := strings.Join(names, "|")
			at, ok := byKey[key]
			if !ok {
				byKey[key] = len(entries)
				entries = append(entries, Entry{Names: names, Demand: demand, Required: line.Required, Line: line.Text, Index: i})
				continue
			}
			e := &entries[at]
			e.Required = e.Required || line.Required
			if defaultVocab.rank(demand) > defaultVocab.rank(e.Demand) {
				e.Demand, e.Line, e.Index = demand, line.Text, i
			}
		}
	}
	return entries, rest
}
