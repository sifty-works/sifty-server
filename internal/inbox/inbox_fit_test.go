package inbox_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/listing"
)

func defaultRules() fit.Rules {
	c := config.Defaults()
	return fit.Rules{Weights: c.Weights, Rates: c.Rates}
}

func fitFixture(t *testing.T) (*db.DB, map[string]int64) {
	t.Helper()
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	p, err := fit.LoadProfile(context.Background(), d, 1, defaultRules())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	stored := map[string]string{"A": "%d,0,'scored',80", "B": "%d,0,'scored',80", "C": "%d,0,'scored',60", "D": "%d,0,'scored',40",
		"E": "%d,0,'scored',90", "G": "%d,0,'no_text',NULL"}
	for _, name := range strings.Split("ABCDEFG", "") {
		ids[name] = add(t, newCat(d), post("https://example.com/jobs/fit"+name, name, "Co"))
		if values, ok := stored[name]; ok {
			inputs := p.Inputs
			if name == "E" {
				inputs++
			}
			d.Exec(fmt.Sprintf(`INSERT INTO fit (profile, vacancy, version, inputs, listing, state, score) VALUES (1,%d,1,`+values+`)`, ids[name], inputs))
		}
	}
	return d, ids
}

func TestBestFitOrdersCurrentNumbersFirstThenDimmedAndUnscored(t *testing.T) {
	d, _ := fitFixture(t)
	ts := newTestServer(t, d)
	body := apiGet(t, ts, "/api/vacancies")
	if got := titles(body); got != "ABCDEFG" {
		t.Errorf("Best fit order %q, want ABCDEFG", got)
	}
	marks := map[string]string{}
	for _, r := range getRows(body) {
		m := r.(map[string]any)["fit"].(map[string]any)
		marks[field(r, "title")] = fmt.Sprintf("%v|%v|%v|%v", m["score"], m["bucket"], m["text"], m["dimmed"])
	}
	for name, want := range map[string]string{
		"A": "80|Strong|Fits you 80: Strong|false", "C": "60|Partial|Fits you 60: Partial|false",
		"D": "40|Weak|Fits you 40: Weak|false", "E": "90|Strong|Fits you 90: Strong|true",
		"F": "<nil>|Not scored|Not compared yet|false", "G": "<nil>|Not scored|Full job description is unavailable|false",
	} {
		if marks[name] != want {
			t.Errorf("row %s: mark %s, want %s", name, marks[name], want)
		}
	}
}

func TestFitFilterSelectsBucketsInEveryListAndDismissedKeepsDecisionOrder(t *testing.T) {
	d, ids := fitFixture(t)
	ts := newTestServer(t, d)
	for filter, want := range map[string]string{
		"strong": "AB", "partial": "C", "weak": "D", "not_scored": "EFG", "strong,weak": "ABD",
	} {
		if got := titles(apiGet(t, ts, "/api/vacancies?fit="+filter)); got != want {
			t.Errorf("fit=%s: rows %q, want %q", filter, got, want)
		}
	}
	body := apiGet(t, ts, "/api/vacancies?fit=strong")
	if sc := body["sourceCounts"].([]any); len(sc) != 1 || sc[0].(map[string]any)["count"] != float64(2) {
		t.Errorf("source counts under the Fit filter: %v", sc)
	}
	decide(t, d, ids["A"], "shortlist", "", "2026-11-01T00:00:00Z")
	decide(t, d, ids["E"], "shortlist", "", "2026-11-02T00:00:00Z")
	decide(t, d, ids["C"], "dismissed", "", "2026-11-01T00:00:00Z")
	decide(t, d, ids["D"], "dismissed", "", "2026-11-02T00:00:00Z")
	if got := titles(apiGet(t, ts, "/api/vacancies?stage=dismissed&sort=fit")); got != "DC" {
		t.Errorf("Dismissed order %q, want DC (newest decision first)", got)
	}
	if got := titles(apiGet(t, ts, "/api/vacancies?stage=shortlist&fit=not_scored")); got != "E" {
		t.Errorf("Shortlist not scored: %q, want E", got)
	}
	if body := apiGet(t, ts, "/api/vacancies?stage=shortlist&fit=weak"); body["empty"] != "No matching roles. Adjust your filters." {
		t.Errorf("empty text under the Fit filter: %v", body["empty"])
	}
}

func TestOutOfDateResultsAreDimmedUntilRecomputed(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, []string{"Backend Engineer"}, []string{"description"}, nil)
	add(t, newCat(d), post("https://example.com/jobs/be", "Senior Backend Engineer", "Co"))
	ts := newServer(t, d, serverOpts{withCandidate: true})
	recompute := func() { fit.Recompute(context.Background(), d, defaultRules, func(int, int, string) {}) }
	mark := func() map[string]any {
		return getRows(apiGet(t, ts, "/api/vacancies"))[0].(map[string]any)["fit"].(map[string]any)
	}
	recompute()
	before := mark()
	if _, status := apiJSON(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{"targetRoles": []string{"Backend Engineer", "Senior Backend Engineer"}}); status != http.StatusOK {
		t.Fatalf("PATCH profile: %d", status)
	}
	if m := mark(); m["dimmed"] != true || before["dimmed"] != false || m["score"] != before["score"] {
		t.Errorf("after a profile change: %v, want the old number dimmed", m)
	}
	recompute()
	if m := mark(); m["dimmed"] != false || m["score"] == before["score"] {
		t.Errorf("after recomputation: %v, want a new current number", m)
	}
	add(t, newCat(d), postWithText("https://example.com/jobs/be", "Senior Backend Engineer", "Co",
		"job description, now onsite in Berlin"))
	if m := mark(); m["dimmed"] != true {
		t.Errorf("after the vacancy changed: %v, want dimmed", m)
	}
}

func TestVacancyPageShowsTheBandWithItsTiles(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, []string{"Backend Engineer"}, []string{"description"}, nil)
	cat := newCat(d)
	full := add(t, cat, post("https://example.com/jobs/full", "Backend Engineer", "Co"))
	summary := postWithText("https://example.com/jobs/short", "Backend Engineer", "Co", "job description")
	summary.TextState = listing.TextSummary
	summaryID := add(t, cat, summary)
	ts := newTestServer(t, d)
	band := func(id int64) map[string]any {
		b, _ := apiGetRaw(t, ts, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
		return b["band"].(map[string]any)
	}
	b := band(full)
	if b["score"] == nil || b["text"] != fmt.Sprintf("Fits you %v /100", b["score"]) || len(b["tiles"].([]any)) != 8 {
		t.Errorf("band of a scored vacancy: %v", b)
	}
	if b := band(summaryID); b["score"] != nil || b["text"] != "Full job description is unavailable" {
		t.Errorf("band without full text: %v", b)
	}
	setProfileData(t, d, nil, []string{"description"}, nil)
	if b := band(full); b["text"] != "Your profile states no goals to compare" || b["bucket"] != "Not scored" {
		t.Errorf("band without goals: %v", b)
	}
}
