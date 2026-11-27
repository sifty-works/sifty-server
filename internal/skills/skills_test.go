package skills

import (
	"testing"
)

func idx(haves, absents []string) *Index { return NewIndex(haves, absents) }

func TestDecide_technologyContextMakesAmbiguousFit(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := "Go programming language"
	if got := Decide(r, i).Verdict; got != "✓" {
		t.Fatalf("want ✓, got %q", got)
	}
}

func TestDecide_ambiguousTermWithoutContextDoesNotMatch(t *testing.T) {
	for _, c := range []struct{ skill, text string }{
		{"Go", "go to market"},
		{"Excel", "Excel in a team"},
	} {
		t.Run(c.skill, func(t *testing.T) {
			i := idx([]string{c.skill}, nil)
			if got := Decide(c.text, i).Verdict; got == "✓" {
				t.Fatalf("%q should not match the %s skill, got %q", c.text, c.skill, got)
			}
		})
	}
}

func TestDecide_unknownWordsInRequirementAreUnclear(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := "Go and Zoflux expertise"
	if got := Decide(r, i).Verdict; got != "?" {
		t.Fatalf("unknown word Zoflux should give ?, got %q", got)
	}
}

func TestDecide_absentSkillGivesNo(t *testing.T) {
	i := idx(nil, []string{"Python"})
	r := "Python"
	if got := Decide(r, i).Verdict; got != "✕" {
		t.Fatalf("absent Python should give ✕, got %q", got)
	}
}

func TestDecide_skillHeldUnderItsOwnNameGivesYes(t *testing.T) {
	for _, name := range []string{"PostgreSQL", "C#", "C++", "Node.js", ".NET"} {
		t.Run(name, func(t *testing.T) {
			i := idx([]string{name}, nil)
			if got := Decide(name, i).Verdict; got != "✓" {
				t.Fatalf("%s should match the %s skill, got %q", name, name, got)
			}
		})
	}
}

func TestDecide_plainTechSkillNotHeld(t *testing.T) {
	i := idx([]string{"MySQL"}, nil)
	r := "PostgreSQL"
	got := Decide(r, i).Verdict
	if got == "✓" {
		t.Fatalf("MySQL should not match PostgreSQL, got %q", got)
	}
}

func TestDecide_multipleSkillsAllHeld(t *testing.T) {
	i := idx([]string{"Docker", "Kubernetes"}, nil)
	r := "Docker and Kubernetes"
	if got := Decide(r, i).Verdict; got != "✓" {
		t.Fatalf("want ✓ when all skills held, got %q", got)
	}
}

func TestDecide_multipleSkillsPartialIsUnclear(t *testing.T) {
	i := idx([]string{"Docker"}, nil)
	r := "Docker and Kubernetes"
	got := Decide(r, i).Verdict
	if got == "✓" {
		t.Fatalf("only Docker held, should not give ✓, got %q", got)
	}
}

func TestDecide_hyphenJoinedAmbiguousTermNotMatched(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := "high-Go system"
	got := Decide(r, i).Verdict
	if got == "✓" {
		t.Fatalf("hyphen-joined ambiguous term should not match, got %q", got)
	}
}

func TestNewIndex_skillNameWithParentheses(t *testing.T) {
	i := idx([]string{"Python (3.x)"}, nil)
	r := "Python"
	if got := Decide(r, i).Verdict; got != "✓" {
		t.Fatalf("skill with paren suffix should still match, got %q", got)
	}
}

func TestDecide_orTokenInTextMeansAnyOf(t *testing.T) {
	i := idx([]string{"Python"}, nil)
	r := "Go or Python"
	if got := Decide(r, i).Verdict; got != "✓" {
		t.Fatalf("or in text: Python held should give ✓, got %q", got)
	}
}

func TestDecide_genericOnlyTextIsUnclear(t *testing.T) {
	i := idx(nil, nil)
	r := "strong experience required"
	if got := Decide(r, i).Verdict; got != "?" {
		t.Fatalf("generic-only text should give ?, got %q", got)
	}
}

func TestDecide_genericQualifierDoesNotBlockNonAmbiguousSkill(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := "Golang expertise"
	if got := Decide(r, i).Verdict; got != "✓" {
		t.Fatalf("want ✓ with generic qualifier, got %q", got)
	}
}

func TestDecide_proficiencyInContextMakesAmbiguousTermAccepted(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	r := "Advanced proficiency in Go"
	if got := Decide(r, i).Verdict; got != "✓" {
		t.Fatalf("want ✓ when proficiency precedes ambiguous term, got %q", got)
	}
}

func TestDecide_nonPrefixBlocksTermMatch(t *testing.T) {
	i := idx([]string{"EVM"}, nil)
	r := "non-EVM chain experience"
	got := Decide(r, i).Verdict
	if got == "✓" {
		t.Fatalf("non- prefix should block match, got %q", got)
	}
}

func TestDecideFull_evidenceForMatchedSkill(t *testing.T) {
	i := idx([]string{"Go"}, nil)
	dr := Decide("Golang expertise", i)
	if dr.Verdict != "✓" {
		t.Fatalf("verdict: got %q, want ✓", dr.Verdict)
	}
	if dr.Evidence != "Go" {
		t.Errorf("evidence: got %q, want %q", dr.Evidence, "Go")
	}
}

func TestDecideFull_evidenceForAbsentSkill(t *testing.T) {
	i := idx(nil, []string{"Python"})
	dr := Decide("Python", i)
	if dr.Verdict != "✕" {
		t.Fatalf("verdict: got %q, want ✕", dr.Verdict)
	}
	if dr.Evidence != "Python" {
		t.Errorf("evidence: got %q, want %q", dr.Evidence, "Python")
	}
}
