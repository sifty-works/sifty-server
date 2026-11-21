package skills

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/reading"
)

func meSummaryOf(me candidate.MeData) string {
	var out []string
	for _, s := range me.Skills {
		out = append(out, fmt.Sprintf("skill %s %s", s.Name, s.Level))
	}
	for _, a := range me.AbsentSkills {
		out = append(out, "absent "+a.Name)
	}
	for _, c := range me.ConfirmedFacts {
		out = append(out, fmt.Sprintf("fact %s %v", c.Text, c.Have))
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

func idx(haves, absents []string) *Index {
	var skills []candidate.Skill
	for _, h := range haves {
		skills = append(skills, candidate.Skill{Name: h})
	}
	var absent []candidate.AbsentSkill
	for _, a := range absents {
		absent = append(absent, candidate.AbsentSkill{Name: a})
	}
	return NewIndex(skills, absent)
}

func req(text, kind string, alts ...string) reading.Requirement {
	return reading.Requirement{Text: text, Kind: kind, Alts: alts}
}

func TestDecide_technologyContextMakesAmbiguousFit(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := req("Go programming language", "skill")
	if got := Decide(r, i, nil); got != "✓" {
		t.Fatalf("want ✓, got %q", got)
	}
}

func TestDecide_ambiguousTermWithoutContextDoesNotMatch(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := req("go to market", "skill")
	got := Decide(r, i, nil)
	if got == "✓" {
		t.Fatalf("go-to-market should not match Go skill, got %q", got)
	}
}

func TestDecide_alternativesMatchAnyHeld(t *testing.T) {
	i := idx([]string{"Python"}, nil)
	r := req("Kotlin, Python or Go", "skill", "Kotlin", "Python", "Go")
	if got := Decide(r, i, nil); got != "✓" {
		t.Fatalf("want ✓ with Python in alts, got %q", got)
	}
}

func TestDecide_alternativesNoneHeld(t *testing.T) {
	i := idx([]string{"Rust"}, nil)
	r := req("Kotlin, Python or Go", "skill", "Kotlin", "Python", "Go")
	got := Decide(r, i, nil)
	if got == "✓" {
		t.Fatalf("Rust not in alts, want not ✓, got %q", got)
	}
}

func TestDecide_unknownWordsInRequirementAreUnclear(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := req("Go and Zoflux expertise", "skill")
	if got := Decide(r, i, nil); got != "?" {
		t.Fatalf("unknown word Zoflux should give ?, got %q", got)
	}
}

func TestDecide_absentSkillGivesNo(t *testing.T) {
	i := idx(nil, []string{"Python"})
	r := req("Python", "skill")
	if got := Decide(r, i, nil); got != "✕" {
		t.Fatalf("absent Python should give ✕, got %q", got)
	}
}

func TestDecide_confirmedHaveGivesYes(t *testing.T) {
	i := idx(nil, nil)
	r := req("security clearance", "skill")
	facts := []candidate.ConfirmedFact{{Text: "security clearance", Have: true}}
	if got := Decide(r, i, facts); got != "✓" {
		t.Fatalf("confirmed fact should give ✓, got %q", got)
	}
}

func TestDecide_confirmedHaveNotGivesNo(t *testing.T) {
	i := idx(nil, nil)
	r := req("driving licence", "skill")
	facts := []candidate.ConfirmedFact{{Text: "driving licence", Have: false}}
	if got := Decide(r, i, facts); got != "✕" {
		t.Fatalf("confirmed not-have should give ✕, got %q", got)
	}
}

func TestDecide_experienceKindIsUnclearUnlessConfirmed(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := req("5+ years backend", "experience")
	if got := Decide(r, i, nil); got != "?" {
		t.Fatalf("experience kind should give ?, got %q", got)
	}
	if got := Decide(r, i, []candidate.ConfirmedFact{{Text: "5+ years backend", Have: true}}); got != "✓" {
		t.Fatalf("confirmed experience should give ✓, got %q", got)
	}
}

func TestDecide_skillHeldUnderItsOwnNameGivesYes(t *testing.T) {
	for _, name := range []string{"PostgreSQL", "C#", "C++", "Node.js", ".NET"} {
		t.Run(name, func(t *testing.T) {
			i := idx([]string{name}, nil)
			if got := Decide(req(name, "skill"), i, nil); got != "✓" {
				t.Fatalf("%s should match the %s skill, got %q", name, name, got)
			}
		})
	}
}

func TestDecide_plainTechSkillNotHeld(t *testing.T) {
	i := idx([]string{"MySQL"}, nil)
	r := req("PostgreSQL", "skill")
	got := Decide(r, i, nil)
	if got == "✓" {
		t.Fatalf("MySQL should not match PostgreSQL, got %q", got)
	}
}

func TestDecide_multipleSkillsAllHeld(t *testing.T) {
	i := idx([]string{"Docker", "Kubernetes"}, nil)
	r := req("Docker and Kubernetes", "skill")
	if got := Decide(r, i, nil); got != "✓" {
		t.Fatalf("want ✓ when all skills held, got %q", got)
	}
}

func TestDecide_multipleSkillsPartialIsUnclear(t *testing.T) {
	i := idx([]string{"Docker"}, nil)
	r := req("Docker and Kubernetes", "skill")
	got := Decide(r, i, nil)
	if got == "✓" {
		t.Fatalf("only Docker held, should not give ✓, got %q", got)
	}
}

func TestDecide_hyphenJoinedAmbiguousTermNotMatched(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := req("high-Go system", "skill")
	got := Decide(r, i, nil)
	if got == "✓" {
		t.Fatalf("hyphen-joined ambiguous term should not match, got %q", got)
	}
}

func TestNewIndex_skillNameWithParentheses(t *testing.T) {
	i := idx([]string{"Python (3.x)"}, nil)
	r := req("Python", "skill")
	if got := Decide(r, i, nil); got != "✓" {
		t.Fatalf("skill with paren suffix should still match, got %q", got)
	}
}

func TestDecide_orTokenInTextMeansAnyOf(t *testing.T) {
	i := idx([]string{"Python"}, nil)
	r := req("Go or Python", "skill")
	if got := Decide(r, i, nil); got != "✓" {
		t.Fatalf("or in text: Python held should give ✓, got %q", got)
	}
}

func TestDecide_genericOnlyTextIsUnclear(t *testing.T) {
	i := idx(nil, nil)
	r := req("strong experience required", "skill")
	if got := Decide(r, i, nil); got != "?" {
		t.Fatalf("generic-only text should give ?, got %q", got)
	}
}

func TestDecide_genericQualifierDoesNotBlockNonAmbiguousSkill(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := req("Golang expertise", "skill")
	if got := Decide(r, i, nil); got != "✓" {
		t.Fatalf("want ✓ with generic qualifier, got %q", got)
	}
}

func TestDecide_proficiencyInContextMakesAmbiguousTermAccepted(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := req("Advanced proficiency in Go", "skill")
	if got := Decide(r, i, nil); got != "✓" {
		t.Fatalf("want ✓ when proficiency precedes ambiguous term, got %q", got)
	}
}

func TestDecide_nonPrefixBlocksTermMatch(t *testing.T) {
	i := idx([]string{"EVM"}, nil)
	r := req("non-EVM chain experience", "skill")
	got := Decide(r, i, nil)
	if got == "✓" {
		t.Fatalf("non- prefix should block match, got %q", got)
	}
}

func TestDecideFull_evidenceForMatchedSkill(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	dr := DecideFull(req("Golang expertise", "skill"), i, nil, candidate.DerivedYears{})
	if dr.Verdict != "✓" {
		t.Fatalf("verdict: got %q, want ✓", dr.Verdict)
	}
	if dr.Evidence != "Go" {
		t.Errorf("evidence: got %q, want %q", dr.Evidence, "Go")
	}
}

func TestDecideFull_evidenceForAbsentSkill(t *testing.T) {
	i := idx(nil, []string{"Python"})
	dr := DecideFull(req("Python", "skill"), i, nil, candidate.DerivedYears{})
	if dr.Verdict != "✕" {
		t.Fatalf("verdict: got %q, want ✕", dr.Verdict)
	}
	if dr.Evidence != "Python" {
		t.Errorf("evidence: got %q, want %q", dr.Evidence, "Python")
	}
}

func TestDecideFull_evidenceForConfirmed(t *testing.T) {
	i := idx(nil, nil)
	facts := []candidate.ConfirmedFact{{Text: "security clearance", Have: true}}
	dr := DecideFull(req("security clearance", "skill"), i, facts, candidate.DerivedYears{})
	if dr.Verdict != "✓" {
		t.Fatalf("verdict: got %q, want ✓", dr.Verdict)
	}
	if dr.Evidence != "Confirmed by you" {
		t.Errorf("evidence: got %q, want %q", dr.Evidence, "Confirmed by you")
	}
}

func TestDecideFull_experienceEvidence(t *testing.T) {
	for _, tc := range []struct {
		text string
		dy   candidate.DerivedYears
		want string
	}{
		{"5+ years leading teams", candidate.DerivedYears{Total: 10, Leading: 5, Engineering: 3}, "5 years"},
		{"3+ years of backend engineering", candidate.DerivedYears{Total: 10, Leading: 5, Engineering: 3}, "3 years"},
		{"8+ years experience", candidate.DerivedYears{Total: 8, Leading: 2, Engineering: 4}, "8 years"},
		{"KYC/KYB experience", candidate.DerivedYears{Total: 10, Leading: 5, Engineering: 3}, ""},
	} {
		dr := DecideFull(req(tc.text, "experience"), nil, nil, tc.dy)
		if dr.Evidence != tc.want {
			t.Errorf("%q: evidence got %q, want %q", tc.text, dr.Evidence, tc.want)
		}
	}
}

func TestDecideFull_namedSkillsFromAlts(t *testing.T) {
	i := idx(nil, nil)
	dr := DecideFull(req("Go or Rust", "skill", "Go", "Rust"), i, nil, candidate.DerivedYears{})
	if len(dr.NamedSkills) < 2 {
		t.Errorf("named skills from alts: got %v, want at least [Go Rust]", dr.NamedSkills)
	}
}

func TestAnswer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		me     candidate.MeData
		r      reading.Requirement
		have   bool
		chosen string
		wantMe string
	}{
		{name: "known skill added at Proficient",
			r: req("Golang expertise", "skill"), have: true,
			wantMe: "skill Go Proficient"},
		{name: "chosen alternative added alone",
			r: req("Go or Rust", "skill", "Go", "Rust"), have: true, chosen: "Rust",
			wantMe: "skill Rust Proficient"},
		{name: "unknown requirement confirmed as held",
			r: req("Strong communication skills", "skill"), have: true,
			wantMe: "fact Strong communication skills true"},
		{name: "unknown requirement confirmed as missing",
			r: req("Strong communication skills", "skill"), have: false,
			wantMe: "fact Strong communication skills false"},
		{name: "plus on absent skill removes it from absent list",
			me: candidate.MeData{AbsentSkills: []candidate.AbsentSkill{{Name: "Docker"}}},
			r:  req("Docker", "skill"), have: true,
			wantMe: "skill Docker Proficient"},
		{name: "minus on held skill moves it to absent list only",
			me: candidate.MeData{Skills: []candidate.Skill{{Name: "Python", Level: "Expert"}}},
			r:  req("Python", "skill"), have: false,
			wantMe: "absent Python"},
		{name: "plus on skill at higher level keeps its level",
			me: candidate.MeData{Skills: []candidate.Skill{{Name: "Go", Level: "Expert"}}},
			r:  req("Golang expertise", "skill"), have: true,
			wantMe: "skill Go Expert"},
		{name: "chosen skill of compound requirement added alone without confirmation",
			r: req("Docker and Kubernetes in production", "skill"), have: true, chosen: "Docker",
			wantMe: "skill Docker Proficient"},
		{name: "minus on chosen skill of compound requirement added alone without confirmation",
			r: req("Docker and Kubernetes in production", "skill"), have: false, chosen: "Docker",
			wantMe: "absent Docker"},
		{name: "compound requirement answered without choice records all skills and confirms",
			r: req("Docker and Kubernetes in production", "skill"), have: true,
			wantMe: "fact Docker and Kubernetes in production true; skill Docker Proficient; skill Kubernetes Proficient"},
		{name: "compound requirement answered with minus records all as absent and confirms",
			r: req("Docker and Kubernetes in production", "skill"), have: false,
			wantMe: "absent Docker; absent Kubernetes; fact Docker and Kubernetes in production false"},
		{name: "single skill in context confirmed too",
			r: req("Kubernetes in production", "skill"), have: true,
			wantMe: "fact Kubernetes in production true; skill Kubernetes Proficient"},
		{name: "single skill in context confirmed when sent by name",
			r: req("Kubernetes in production", "skill"), have: true, chosen: "Kubernetes",
			wantMe: "fact Kubernetes in production true; skill Kubernetes Proficient"},
		{name: "single skill in context confirmed as missing",
			r: req("Kubernetes in production", "skill"), have: false,
			wantMe: "absent Kubernetes; fact Kubernetes in production false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Answer(tc.r, tc.have, tc.chosen, tc.me)
			if got := meSummaryOf(res.Me); got != tc.wantMe {
				t.Errorf("Me: got %q, want %q", got, tc.wantMe)
			}
		})
	}
}
