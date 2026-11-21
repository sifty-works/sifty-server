package industry_test

import (
	"testing"

	"github.com/sifty-works/sifty-server/internal/industry"
)

func TestFromLabels_KnownLabelsMapToIndustries(t *testing.T) {
	for _, labels := range [][]string{
		{"Blockchain", "Fintech"},
		{"Blockchain", "DeFi"},
	} {
		got := industry.FromLabels(labels)
		want := map[string]bool{"Web3": true, "Finance": true}
		if len(got) != 2 {
			t.Fatalf("FromLabels(%v): got %v, want [Web3 Finance]", labels, got)
		}
		for _, g := range got {
			if !want[g] {
				t.Errorf("FromLabels(%v): unexpected industry %q", labels, g)
			}
		}
	}
}

func TestFromLabels_UnknownLabelReturnsNil(t *testing.T) {
	got := industry.FromLabels([]string{"NoSuchLabel"})
	if got != nil {
		t.Errorf("unknown label: got %v, want nil", got)
	}
}

func TestFromLabels_CaseInsensitiveMatch(t *testing.T) {
	got := industry.FromLabels([]string{"blockchain"})
	if len(got) != 1 || got[0] != "Web3" {
		t.Errorf("case insensitive: got %v, want [Web3]", got)
	}
}

func TestNames_ContainsAllFifteenIndustries(t *testing.T) {
	names := industry.Names()
	want := []string{
		"Software", "Web3", "Security", "Data and AI", "Finance",
		"Healthcare", "Education", "E-commerce and retail",
		"Media and communications", "Gaming", "Hardware and energy",
		"Government and public sector", "Agriculture",
		"Architecture and construction", "Photo and video",
	}
	if len(names) != len(want) {
		t.Fatalf("Names: got %d, want %d", len(names), len(want))
	}
	nm := make(map[string]bool, len(names))
	for _, n := range names {
		nm[n] = true
	}
	for _, w := range want {
		if !nm[w] {
			t.Errorf("Names: missing %q", w)
		}
	}
}
