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
)

func names[T, R any](xs []T, name func(T) R) []R {
	out := make([]R, len(xs))
	for i, x := range xs {
		out[i] = name(x)
	}
	return out
}

func normalizeKey(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
	return strings.Trim(s, ".,;:!?–—-'\"()")
}

func decide(text string, confirmed []candidate.ConfirmedFact, dy candidate.DerivedYears) (verdict, evidence string) {
	key := normalizeKey(text)
	for _, c := range confirmed {
		if normalizeKey(c.Text) == key {
			if c.Have {
				return "✓", "Confirmed by you"
			}
			return "✕", "Confirmed by you"
		}
	}
	if _, ok := skills.YearsIn(text); ok {
		return "?", yearsEvidence(text, dy)
	}
	return "?", ""
}

func readRequirements(p ProfileView, v Vacancy) ([]SkillEntry, []RequirementLine) {
	if !v.Analysed {
		return nil, nil
	}
	lines := make([]skills.Line, len(v.Requirements))
	for i, r := range v.Requirements {
		lines[i] = skills.Line{Text: r.Text, Required: r.Level == "required"}
	}
	entries, rest := skills.Entries(lines)
	idx := skills.NewIndex(
		names(p.Skills, func(s candidate.Skill) skills.Held { return skills.Held{Name: s.Name, Level: s.Level} }),
		names(p.AbsentSkills, func(s candidate.AbsentSkill) string { return s.Name }))
	var entriesOut []SkillEntry
	for _, e := range entries {
		out := SkillEntry{Demand: e.Demand, Line: e.Line, Verdict: idx.Verdict(e), Required: e.Required, Index: e.Index}
		for _, name := range e.Names {
			have := idx.Have(name)
			if have == skills.Listed {
				have = ""
			}
			out.Skills = append(out.Skills, SkillHave{Name: name, Have: have})
		}
		entriesOut = append(entriesOut, out)
	}
	var rows []RequirementLine
	for _, i := range rest {
		req := v.Requirements[i]
		verdict, evidence := decide(req.Text, p.Confirmed, p.DerivedYears)
		rows = append(rows, RequirementLine{Index: i, Text: req.Text, Level: req.Level, Verdict: verdict, Evidence: evidence})
	}
	return entriesOut, rows
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

func IsLevel(level string) bool {
	return level == skills.No || slices.Contains(skills.Levels(), level)
}

func ConfirmRow(text string, have bool, me candidate.MeData) (candidate.MeData, bool) {
	if slices.ContainsFunc(me.ConfirmedFacts, func(f candidate.ConfirmedFact) bool { return f.Text == text }) {
		return me, false
	}
	me.ConfirmedFacts = append(me.ConfirmedFacts, candidate.ConfirmedFact{Text: text, Have: have})
	return me, true
}

func SetLevel(skill, level string, me candidate.MeData) (candidate.MeData, bool) {
	sameSkill := func(name string) bool {
		return strings.EqualFold(name, skill) || slices.ContainsFunc(skills.Groups(name), func(g string) bool { return strings.EqualFold(g, skill) })
	}
	if level == skills.No {
		held := len(me.Skills)
		me.Skills = slices.DeleteFunc(me.Skills, func(s candidate.Skill) bool { return sameSkill(s.Name) })
		changed := len(me.Skills) != held
		if !slices.ContainsFunc(me.AbsentSkills, func(a candidate.AbsentSkill) bool { return sameSkill(a.Name) }) {
			me.AbsentSkills = append(me.AbsentSkills, candidate.AbsentSkill{Name: skill})
			changed = true
		}
		return me, changed
	}
	absent := len(me.AbsentSkills)
	me.AbsentSkills = slices.DeleteFunc(me.AbsentSkills, func(a candidate.AbsentSkill) bool { return sameSkill(a.Name) })
	changed := len(me.AbsentSkills) != absent
	held := false
	for i, s := range me.Skills {
		if !sameSkill(s.Name) {
			continue
		}
		held = true
		if s.Level != level {
			me.Skills[i].Level, changed = level, true
		}
	}
	if !held {
		me.Skills = append(me.Skills, candidate.Skill{Name: skill, Level: level})
		changed = true
	}
	return me, changed
}
