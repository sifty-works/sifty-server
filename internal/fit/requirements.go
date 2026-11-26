package fit

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/skills"
)

var (
	leadKeyRe = regexp.MustCompile(`(?i)\b(lead|leading|manage|managing|management|head|director|vp|cto|ceo|coo|president)\b`)
	engKeyRe  = regexp.MustCompile(`(?i)\b(engineer|engineering|developer|architect|programmer|technical|backend|frontend|software)\b`)
	yearsInRe = regexp.MustCompile(`(?i)\b\d+\+?\s+years?\b`)
)

func names[T any](xs []T, name func(T) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = name(x)
	}
	return out
}

func normalizeKey(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
	return strings.Trim(s, ".,;:!?–—-'\"()")
}

func decide(text string, idx *skills.Index, confirmed []candidate.ConfirmedFact, dy candidate.DerivedYears) skills.DecisionResult {
	key := normalizeKey(text)
	for _, c := range confirmed {
		if normalizeKey(c.Text) == key {
			v := "✓"
			if !c.Have {
				v = "✕"
			}
			return skills.DecisionResult{Verdict: v, Evidence: "Confirmed by you"}
		}
	}
	if yearsInRe.MatchString(text) {
		return skills.DecisionResult{Verdict: "?", Evidence: yearsEvidence(text, dy)}
	}
	return skills.Decide(text, idx)
}

func yearsEvidence(text string, dy candidate.DerivedYears) string {
	years := dy.Total
	if leadKeyRe.MatchString(text) {
		years = dy.Leading
	} else if engKeyRe.MatchString(text) {
		years = dy.Engineering
	}
	switch {
	case years <= 0:
		return ""
	case years == 1:
		return "1 year"
	}
	return fmt.Sprintf("%d years", years)
}

type AnswerResult struct {
	Me             candidate.MeData
	AddedSkills    []string
	MovedSkills    []string
	AddedConfirmed []string
}

func Answer(text string, have bool, chosenSkill string, me candidate.MeData) AnswerResult {
	named := skills.NamedSkills(text)
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
		eqText := func(f candidate.ConfirmedFact) bool { return f.Text == text }
		if !slices.ContainsFunc(me.ConfirmedFacts, eqText) {
			me.ConfirmedFacts = append(me.ConfirmedFacts, candidate.ConfirmedFact{Text: text, Have: have})
			addedConfirmed = append(addedConfirmed, text)
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
			idx := skills.NewIndex(names(me.Skills, func(s candidate.Skill) string { return s.Name }), names(me.AbsentSkills, func(s candidate.AbsentSkill) string { return s.Name }))
			if decide(text, idx, me.ConfirmedFacts, candidate.DerivedYears{}).Verdict != want {
				addConfirmed()
			}
		}
	} else {
		addConfirmed()
	}
	return AnswerResult{Me: me, AddedSkills: addedSkills, MovedSkills: movedSkills, AddedConfirmed: addedConfirmed}
}
