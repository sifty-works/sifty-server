package skills

import (
	_ "embed"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

//go:embed vocab.yaml
var VocabBytes []byte

type vocabData struct {
	TechnologyContext []string    `yaml:"technology_context"`
	GenericWords      []string    `yaml:"generic_words"`
	Groups            []groupData `yaml:"groups"`
}

type groupData struct {
	Name           string   `yaml:"name"`
	Terms          []string `yaml:"terms"`
	AmbiguousTerms []string `yaml:"ambiguous_terms"`
}

type termInfo struct {
	group     string
	ambiguous bool
	exact     string
}

type vocab struct {
	terms    map[string]termInfo
	maxWords int
	context  map[string]bool
	generic  map[string]bool
}

var defaultVocab = func() *vocab {
	var d vocabData
	if err := yaml.Unmarshal(VocabBytes, &d); err != nil {
		panic("skills: load vocab.yaml: " + err.Error())
	}
	v := &vocab{terms: map[string]termInfo{}, context: map[string]bool{}, generic: map[string]bool{}, maxWords: 1}
	for _, w := range d.TechnologyContext {
		v.context[strings.ToLower(strings.TrimSpace(w))] = true
	}
	for _, w := range d.GenericWords {
		v.generic[strings.ToLower(strings.TrimSpace(w))] = true
	}
	add := func(group, term string, ambiguous bool) {
		toks := tokenize(term)
		if len(toks) == 0 {
			return
		}
		key := joinLower(toks)
		if len(toks) > v.maxWords {
			v.maxWords = len(toks)
		}
		v.terms[key] = termInfo{group, ambiguous, strings.TrimSpace(term)}
	}
	for _, g := range d.Groups {
		ambig := map[string]bool{}
		for _, t := range g.AmbiguousTerms {
			ambig[strings.ToLower(strings.TrimSpace(t))] = true
		}
		if !ambig[strings.ToLower(strings.TrimSpace(g.Name))] {
			add(g.Name, g.Name, false)
		}
		for _, t := range g.Terms {
			add(g.Name, t, false)
		}
		for _, t := range g.AmbiguousTerms {
			add(g.Name, t, true)
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
				((c == '.' || c == '&') && j+csz < len(s) && isWordRune(firstRune(s[j+csz:])) && isWordRune(prev)) {
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

func (v *vocab) scan(text string) (hits []hit, rejected []int, toks []token, covered []bool) {
	toks = tokenize(text)
	covered = make([]bool, len(toks))
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
			if i > 0 && toks[i-1].lower == "non" {
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
		ok := t.text == info.exact && !joinedByHyphen(text, t.start, t.end)
		accepted := ok && whole
		if ok && !whole {
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
		} else {
			rejected = append(rejected, h.from)
		}
	}
	return
}

func joinedByHyphen(text string, start, end int) bool {
	return (start > 0 && text[start-1] == '-') || (end < len(text) && text[end] == '-')
}

func (v *vocab) scanName(name string) []hit {
	toks := tokenize(strings.TrimSpace(name))
	if len(toks) == 0 {
		return nil
	}
	if info, ok := v.terms[joinLower(toks)]; ok {
		return []hit{{info.group, 0, len(toks)}}
	}
	hits, _, _, _ := v.scan(name)
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

type Index struct {
	byGroup     map[string]bool
	absentGroup map[string]bool
	skillNames  map[string][]string
	absentNames map[string][]string
}

func NewIndex(held, absent []string) *Index {
	idx := &Index{
		byGroup: map[string]bool{}, absentGroup: map[string]bool{},
		skillNames: map[string][]string{}, absentNames: map[string][]string{},
	}
	for _, name := range held {
		for _, g := range groupsOf(defaultVocab.scanName(name)) {
			idx.byGroup[g] = true
			idx.skillNames[g] = append(idx.skillNames[g], name)
		}
	}
	for _, name := range absent {
		for _, g := range groupsOf(defaultVocab.scanName(name)) {
			idx.absentGroup[g] = true
			idx.absentNames[g] = append(idx.absentNames[g], name)
		}
	}
	return idx
}

type DecisionResult struct {
	Verdict     string
	Evidence    string
	NamedSkills []string
}

func groups(text string) (named []string, anyOf bool, canDecide bool) {
	hits, rejected, toks, covered := defaultVocab.scan(text)
	named = groupsOf(hits)
	orPresent := false
	leftover := len(rejected) > 0
	for k, t := range toks {
		if covered[k] {
			continue
		}
		if t.lower == "or" {
			orPresent = true
			continue
		}
		if len([]rune(t.lower)) < 3 || defaultVocab.generic[t.lower] || defaultVocab.context[t.lower] {
			continue
		}
		leftover = true
	}
	return named, orPresent, !(leftover && !orPresent)
}

func Decide(text string, idx *Index) DecisionResult {
	named, anyOf, canDecide := groups(text)
	if !canDecide || len(named) == 0 {
		return DecisionResult{Verdict: "?", NamedSkills: named}
	}
	v := verdictForGroups(named, anyOf, idx)
	var ev []string
	switch v {
	case "✓":
		for _, g := range named {
			ev = append(ev, idx.skillNames[g]...)
		}
	case "✕":
		for _, g := range named {
			ev = append(ev, idx.absentNames[g]...)
		}
	}
	return DecisionResult{Verdict: v, Evidence: joinEvidence(ev), NamedSkills: named}
}

func NamedSkills(text string) []string {
	named, _, _ := groups(text)
	return named
}

func joinEvidence(names []string) string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return strings.Join(out, " · ")
}

func verdictForGroups(gs []string, anyOf bool, idx *Index) string {
	nHave, nAbsent := 0, 0
	for _, g := range gs {
		if idx.byGroup[g] {
			nHave++
		}
		if idx.absentGroup[g] {
			nAbsent++
		}
	}
	if (anyOf && nHave > 0) || (!anyOf && nHave == len(gs)) {
		return "✓"
	}
	if nAbsent == len(gs) {
		return "✕"
	}
	return "?"
}
