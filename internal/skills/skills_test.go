package skills

import (
	"reflect"
	"testing"
)

func held(names ...string) []Held {
	var out []Held
	for _, n := range names {
		out = append(out, Held{Name: n, Level: "Expert"})
	}
	return out
}

func decide(line string, haves, absents []string) string {
	entries, _ := Entries([]Line{{Text: line, Required: true}})
	if len(entries) == 0 {
		return "?"
	}
	return NewIndex(held(haves...), absents).Verdict(entries[0])
}

func entriesOf(lines ...string) ([]Entry, []int) {
	var in []Line
	for _, l := range lines {
		in = append(in, Line{Text: l, Required: true})
	}
	return Entries(in)
}

func TestDecide_technologyContextMakesAmbiguousFit(t *testing.T) {
	if got := decide("Go programming language", []string{"Go"}, nil); got != "✓" {
		t.Fatalf("want ✓, got %q", got)
	}
}

func TestDecide_ambiguousTermWithoutContextDoesNotMatch(t *testing.T) {
	for _, c := range []struct{ skill, text string }{
		{"Go", "go to market"},
		{"Excel", "Excel in a team"},
	} {
		t.Run(c.skill, func(t *testing.T) {
			if got := decide(c.text, []string{c.skill}, nil); got == "✓" {
				t.Fatalf("%q should not match the %s skill, got %q", c.text, c.skill, got)
			}
		})
	}
}

func TestDecide_unknownWordsInRequirementAreUnclear(t *testing.T) {
	if got := decide("Go and Zoflux expertise", []string{"Go"}, nil); got != "✓" {
		t.Fatalf("an unknown word must not make the Go skill unclear, got %q", got)
	}
}

func TestDecide_absentSkillGivesNo(t *testing.T) {
	if got := decide("Python", nil, []string{"Python"}); got != "✕" {
		t.Fatalf("absent Python should give ✕, got %q", got)
	}
}

func TestDecide_skillHeldUnderItsOwnNameGivesYes(t *testing.T) {
	for _, name := range []string{"PostgreSQL", "C#", "C++", "Node.js", ".NET"} {
		t.Run(name, func(t *testing.T) {
			if got := decide(name, []string{name}, nil); got != "✓" {
				t.Fatalf("%s should match the %s skill, got %q", name, name, got)
			}
		})
	}
}

func TestDecide_plainTechSkillNotHeld(t *testing.T) {
	if got := decide("PostgreSQL", []string{"MySQL"}, nil); got == "✓" {
		t.Fatalf("MySQL should not match PostgreSQL, got %q", got)
	}
}

func TestDecide_multipleSkillsAllHeld(t *testing.T) {
	entries, _ := entriesOf("Docker and Kubernetes")
	idx := NewIndex(held("Docker", "Kubernetes"), nil)
	if len(entries) != 2 || idx.Verdict(entries[0]) != "✓" || idx.Verdict(entries[1]) != "✓" {
		t.Fatalf("want two entries, both ✓, got %+v", entries)
	}
}

func TestDecide_multipleSkillsPartialIsUnclear(t *testing.T) {
	entries, _ := entriesOf("Docker and Kubernetes")
	idx := NewIndex(held("Docker"), nil)
	if len(entries) != 2 || idx.Verdict(entries[0]) != "✓" || idx.Verdict(entries[1]) != "?" {
		t.Fatalf("want Docker ✓ and Kubernetes ?, got %+v", entries)
	}
}

func TestDecide_hyphenJoinedAmbiguousTermNotMatched(t *testing.T) {
	if got := decide("high-Go system", []string{"Go"}, nil); got == "✓" {
		t.Fatalf("hyphen-joined ambiguous term should not match, got %q", got)
	}
}

func TestNewIndex_skillNameWithParentheses(t *testing.T) {
	if got := decide("Python", []string{"Python (3.x)"}, nil); got != "✓" {
		t.Fatalf("skill with paren suffix should still match, got %q", got)
	}
}

func TestDecide_orTokenInTextMeansAnyOf(t *testing.T) {
	entries, _ := entriesOf("Go or Python")
	if len(entries) != 1 || NewIndex(held("Python"), nil).Verdict(entries[0]) != "✓" {
		t.Fatalf("Python held should meet Go or Python, got %+v", entries)
	}
}

func TestDecide_genericOnlyTextIsUnclear(t *testing.T) {
	entries, rest := entriesOf("strong experience required")
	if len(entries) != 0 || len(rest) != 1 {
		t.Fatalf("generic-only line should name no skill, got %+v rest %v", entries, rest)
	}
}

func TestDecide_genericQualifierDoesNotBlockNonAmbiguousSkill(t *testing.T) {
	if got := decide("Golang expertise", []string{"Go"}, nil); got != "✓" {
		t.Fatalf("want ✓ with generic qualifier, got %q", got)
	}
}

func TestDecide_proficiencyInContextMakesAmbiguousTermAccepted(t *testing.T) {
	if got := decide("Advanced proficiency in Go", []string{"Go"}, nil); got != "✓" {
		t.Fatalf("want ✓ when proficiency precedes ambiguous term, got %q", got)
	}
}

func TestDecide_nonPrefixBlocksTermMatch(t *testing.T) {
	if got := decide("non-EVM chain experience", []string{"EVM"}, nil); got == "✓" {
		t.Fatalf("non- prefix should block match, got %q", got)
	}
}

func TestDemandIsReadFromYearsAndWordsOfTheLine(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"4 years of Go", "Expert"},
		{"3+ years of Go", "Proficient"},
		{"1+ years of Go", "Beginner"},
		{"Go", "Any"},
		{"2-4 years of Go", "Proficient"},
		{"Strong Go, 5+ years", "Expert"},
	} {
		t.Run(c.line, func(t *testing.T) {
			entries, _ := entriesOf(c.line)
			if len(entries) != 1 || entries[0].Demand != c.want {
				t.Fatalf("demand = %+v, want %s", entries, c.want)
			}
		})
	}
	entries, _ := entriesOf("3+ years of Go and Docker")
	if len(entries) != 2 || entries[0].Demand != "Proficient" || entries[1].Demand != "Proficient" {
		t.Errorf("years must apply to every skill of the line, got %+v", entries)
	}
}

func TestSkillNamedInSeveralLinesIsOneEntryWithTheHighestDemand(t *testing.T) {
	entries, rest := Entries([]Line{
		{Text: "Go", Required: false},
		{Text: "Docker", Required: false},
		{Text: "5+ years of Go", Required: true},
	})
	want := []Entry{
		{Names: []string{"Go"}, Demand: "Expert", Required: true, Line: "5+ years of Go", Index: 2},
		{Names: []string{"Docker"}, Demand: "Any", Required: false, Line: "Docker", Index: 1},
	}
	if !reflect.DeepEqual(entries, want) || len(rest) != 0 {
		t.Fatalf("entries = %+v rest %v, want %+v", entries, rest, want)
	}
}

func TestAlternativesAreOneEntryNamingThemAndSeparateSkillsAreSeparateEntries(t *testing.T) {
	for _, c := range []struct {
		line string
		want [][]string
	}{
		{"Rust, C++ or Python", [][]string{{"Rust", "C++", "Python"}}},
		{"Docker, Kubernetes", [][]string{{"Docker"}, {"Kubernetes"}}},
		{"Python or Go, Docker", [][]string{{"Python", "Go"}, {"Docker"}}},
		{"Proficiency in one or more of Rust, TypeScript, Python or Move", [][]string{{"Rust", "TypeScript", "Python"}}},
		{"JavaScript/TypeScript", [][]string{{"JavaScript", "TypeScript"}}},
		{"Hands-on experience with crypto wallets, blockchain, or related Web3 products", [][]string{{"Crypto wallets", "Blockchain", "Web3"}}},
	} {
		t.Run(c.line, func(t *testing.T) {
			entries, _ := entriesOf(c.line)
			var got [][]string
			for _, e := range entries {
				got = append(got, e.Names)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("entries = %v, want %v", got, c.want)
			}
		})
	}
	entries, rest := entriesOf("Strong communication skills", "Docker")
	if len(entries) != 1 || !reflect.DeepEqual(rest, []int{0}) {
		t.Errorf("a line naming no skill must stay a row: entries %+v rest %v", entries, rest)
	}
}

func TestVerdictCompareDemandedAndOwnLevel(t *testing.T) {
	level := func(name, l string) Held { return Held{Name: name, Level: l} }
	for _, c := range []struct {
		name   string
		entry  Entry
		held   []Held
		absent []string
		want   string
	}{
		{"at the demanded level", Entry{Names: []string{"Go"}, Demand: "Proficient"}, []Held{level("Go", "Proficient")}, nil, "✓"},
		{"above the demanded level", Entry{Names: []string{"Go"}, Demand: "Beginner"}, []Held{level("Go", "Expert")}, nil, "✓"},
		{"one level below", Entry{Names: []string{"Go"}, Demand: "Expert"}, []Held{level("Go", "Proficient")}, nil, "≈"},
		{"two levels below", Entry{Names: []string{"Go"}, Demand: "Expert"}, []Held{level("Go", "Beginner")}, nil, "✕"},
		{"any is met by any level", Entry{Names: []string{"Go"}, Demand: "Any"}, []Held{level("Go", "Beginner")}, nil, "✓"},
		{"a skill the user does not have", Entry{Names: []string{"Go"}, Demand: "Any"}, nil, []string{"Go"}, "✕"},
		{"a skill not in My data", Entry{Names: []string{"Go"}, Demand: "Beginner"}, nil, nil, "?"},
		{"alternatives are met by the best", Entry{Names: []string{"Go", "Rust"}, Demand: "Expert"}, []Held{level("Go", "Beginner"), level("Rust", "Expert")}, nil, "✓"},
		{"alternatives with one not in My data", Entry{Names: []string{"Go", "Rust"}, Demand: "Expert"}, []Held{level("Go", "Beginner")}, nil, "?"},
		{"alternatives all not had", Entry{Names: []string{"Go", "Rust"}, Demand: "Any"}, nil, []string{"Go", "Rust"}, "✕"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := NewIndex(c.held, c.absent).Verdict(c.entry); got != c.want {
				t.Fatalf("verdict = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNamesThatAreNotSkillsAreIgnored(t *testing.T) {
	for _, c := range []struct {
		line string
		want [][]string
	}{
		{"Experience with Unity Catalog", nil},
		{"Experience with Azure Key Vault", [][]string{{"Azure"}}},
		{"Kanban Labs is hiring engineers", nil},
		{"Candidates who react quickly to incidents", nil},
		{"Strong Python foundation and Kanban experience", [][]string{{"Python"}, {"Kanban"}}},
	} {
		t.Run(c.line, func(t *testing.T) {
			entries, _ := entriesOf(c.line)
			var got [][]string
			for _, e := range entries {
				got = append(got, e.Names)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("entries = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDottedNamesInCapitalsStayOneSkillEach(t *testing.T) {
	entries, _ := entriesOf("Node.JS and Vue.Js services")
	var got [][]string
	for _, e := range entries {
		got = append(got, e.Names)
	}
	if want := [][]string{{"Node.js"}, {"Vue"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}
