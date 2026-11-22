package skills

import (
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/reading"
)

var (
	leadKeyRe     = regexp.MustCompile(`(?i)\b(lead|leading|manage|managing|management|head|director|vp|cto|ceo|coo|president)\b`)
	engKeyRe      = regexp.MustCompile(`(?i)\b(engineer|engineering|developer|architect|programmer|technical|backend|frontend|software)\b`)
	yearsInTextRe = regexp.MustCompile(`(?i)\b\d+\+?\s+years?\b`)
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

func NewIndex(skills []candidate.Skill, absent []candidate.AbsentSkill) *Index {
	idx := &Index{
		byGroup: map[string]bool{}, absentGroup: map[string]bool{},
		skillNames: map[string][]string{}, absentNames: map[string][]string{},
	}
	for _, s := range skills {
		for _, g := range groupsOf(defaultVocab.scanName(s.Name)) {
			idx.byGroup[g] = true
			idx.skillNames[g] = append(idx.skillNames[g], s.Name)
		}
	}
	for _, a := range absent {
		for _, g := range groupsOf(defaultVocab.scanName(a.Name)) {
			idx.absentGroup[g] = true
			idx.absentNames[g] = append(idx.absentNames[g], a.Name)
		}
	}
	return idx
}

func normalizeKey(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
	return strings.Trim(s, ".,;:!?–—-'\"()")
}

type DecisionResult struct {
	Verdict     string
	Evidence    string
	NamedSkills []string
}

func reqGroups(req reading.Requirement) (named []string, anyOf bool, canDecide bool) {
	if len(req.Alts) > 0 {
		for _, alt := range req.Alts {
			named = append(named, groupsOf(defaultVocab.scanName(alt))...)
		}
		return named, true, true
	}
	hits, rejected, toks, covered := defaultVocab.scan(req.Text)
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

func DecideFull(req reading.Requirement, idx *Index, confirmed []candidate.ConfirmedFact, dy candidate.DerivedYears) DecisionResult {
	key := normalizeKey(req.Text)
	for _, c := range confirmed {
		if normalizeKey(c.Text) == key {
			v := "✓"
			if !c.Have {
				v = "✕"
			}
			return DecisionResult{Verdict: v, Evidence: "Confirmed by you"}
		}
	}
	if req.Kind == "experience" {
		return DecisionResult{Verdict: "?", Evidence: experienceEvidence(req.Text, dy)}
	}
	named, anyOf, canDecide := reqGroups(req)
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

func Decide(req reading.Requirement, idx *Index, confirmed []candidate.ConfirmedFact) string {
	return DecideFull(req, idx, confirmed, candidate.DerivedYears{}).Verdict
}

func NamedSkillsFor(req reading.Requirement) []string {
	named, _, _ := reqGroups(req)
	return named
}

func experienceEvidence(text string, dy candidate.DerivedYears) string {
	if !yearsInTextRe.MatchString(text) {
		return ""
	}
	years := dy.Total
	if leadKeyRe.MatchString(text) {
		years = dy.Leading
	} else if engKeyRe.MatchString(text) {
		years = dy.Engineering
	}
	if years <= 0 {
		return ""
	}
	if years == 1 {
		return "1 year"
	}
	return fmt.Sprintf("%d years", years)
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

type AnswerResult struct {
	Me             candidate.MeData
	AddedSkills    []string
	MovedSkills    []string
	AddedConfirmed []string
}

func Answer(req reading.Requirement, have bool, chosenSkill string, me candidate.MeData) AnswerResult {
	named := NamedSkillsFor(req)
	toAct := named
	if chosenSkill != "" {
		toAct = []string{chosenSkill}
	}
	eqName := func(n string) func(candidate.Skill) bool {
		return func(s candidate.Skill) bool { return strings.EqualFold(s.Name, n) }
	}
	eqAbsent := func(n string) func(candidate.AbsentSkill) bool {
		return func(a candidate.AbsentSkill) bool { return strings.EqualFold(a.Name, n) }
	}
	var addedSkills, movedSkills, addedConfirmed []string
	addConfirmed := func() {
		eqText := func(f candidate.ConfirmedFact) bool { return f.Text == req.Text }
		if !slices.ContainsFunc(me.ConfirmedFacts, eqText) {
			me.ConfirmedFacts = append(me.ConfirmedFacts, candidate.ConfirmedFact{Text: req.Text, Have: have})
			addedConfirmed = append(addedConfirmed, req.Text)
		}
	}
	if len(toAct) > 0 {
		for _, name := range toAct {
			if have {
				alreadyHeld := slices.ContainsFunc(me.Skills, eqName(name))
				if !alreadyHeld {
					me.Skills = append(me.Skills, candidate.Skill{Name: name, Level: "Proficient"})
				}
				before := len(me.AbsentSkills)
				me.AbsentSkills = slices.DeleteFunc(me.AbsentSkills, eqAbsent(name))
				switch {
				case len(me.AbsentSkills) < before:
					movedSkills = append(movedSkills, name)
				case !alreadyHeld:
					addedSkills = append(addedSkills, name)
				}
			} else {
				alreadyAbsent := slices.ContainsFunc(me.AbsentSkills, eqAbsent(name))
				if !alreadyAbsent {
					me.AbsentSkills = append(me.AbsentSkills, candidate.AbsentSkill{Name: name})
				}
				before := len(me.Skills)
				me.Skills = slices.DeleteFunc(me.Skills, eqName(name))
				switch {
				case len(me.Skills) < before:
					movedSkills = append(movedSkills, name)
				case !alreadyAbsent:
					addedSkills = append(addedSkills, name)
				}
			}
		}
		if chosenSkill == "" || len(named) <= 1 {
			want := "✓"
			if !have {
				want = "✕"
			}
			if Decide(req, NewIndex(me.Skills, me.AbsentSkills), me.ConfirmedFacts) != want {
				addConfirmed()
			}
		}
	} else {
		addConfirmed()
	}
	return AnswerResult{Me: me, AddedSkills: addedSkills, MovedSkills: movedSkills, AddedConfirmed: addedConfirmed}
}
