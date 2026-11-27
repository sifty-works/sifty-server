package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
)

type mustVacancy struct{ title, text string }

func serveWithVacancies(t *testing.T, profile, me string, vs []mustVacancy) (string, []int64) {
	t.Helper()
	dir := t.TempDir()
	seed, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := seed.Exec(`UPDATE profile SET data=? WHERE id=1`, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`UPDATE me SET data=? WHERE id=1`, me); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i, v := range vs {
		id, _, err := catalog.New(seed, time.Now).Upsert(context.Background(), listing.Posting{
			URL: fmt.Sprintf("https://jobs.example.com/must%d", i), Title: v.title, Company: "Stackworks", TextState: listing.TextFull, Text: v.text,
		}, "src")
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		ids = append(ids, id)
	}
	seed.Close()
	port, cancel := startServer(t, dir, true)
	t.Cleanup(cancel)
	probe, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	t.Cleanup(func() { probe.Close() })
	waitFor(t, 10*time.Second, func() bool {
		var scored, active int
		probe.QueryRow(`SELECT count(*) FROM fit WHERE state='scored'`).Scan(&scored)
		probe.QueryRow(`SELECT count(*) FROM job WHERE state IN ('queued','running')`).Scan(&active)
		return scored == len(vs) && active == 0
	})
	return fmt.Sprintf("http://127.0.0.1:%d", port), ids
}

func bandOf(t *testing.T, base string, id int64) map[string]any {
	t.Helper()
	view, status := apiGet(t, base, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
	band, _ := view["band"].(map[string]any)
	if status != 200 || band == nil {
		t.Fatalf("vacancy %d: status %d, view %v", id, status, view)
	}
	return band
}

func reasonsOf(band map[string]any) []string {
	var out []string
	rs, _ := band["reasons"].([]any)
	for _, r := range rs {
		out = append(out, fmt.Sprint(r))
	}
	return out
}

func TestOnsiteOnlyVacancyAgainstRemoteContractProfileIsCappedAndListedLast(t *testing.T) {
	salary := "<p>Salary: EUR 250,000 per year</p>"
	base, ids := serveWithVacancies(t,
		`{"name":"Tester","targetRoles":["Engineer"],"workModes":["remote"],"engagement":["contract"],"compensation":{"cashMin":200000,"currency":"EUR","period":"year"}}`, `{}`,
		[]mustVacancy{
			{"Platform Engineer", "<p>Work mode: Onsite</p><p>Employment type: Contract</p>" + salary},
			{"Staff Principal Distinguished Platform Infrastructure Reliability Software Cloud Data Security Applications Systems Engineer", "<p>Employment type: Employment</p><p>Salary: EUR 20,000 per year</p>"},
			{"Platform Engineer", "<p>Work mode: Remote</p><p>Employment type: Contract</p>" + salary},
		})
	onsite, poor, good := ids[0], ids[1], ids[2]
	band := bandOf(t, base, onsite)
	reasons := reasonsOf(band)
	if score, _ := band["score"].(float64); score > 15 || band["brokenMust"] != true || len(reasons) == 0 || reasons[0] != "Onsite only — you want remote" {
		t.Errorf("onsite vacancy: score %v, brokenMust %v, reasons %q; want at most 15, broken, the onsite reason first", band["score"], band["brokenMust"], reasons)
	}
	if poorScore, _ := bandOf(t, base, poor)["score"].(float64); poorScore >= 15 || poorScore == 0 {
		t.Fatalf("the vacancy without a broken must scores %v, want a number below 15", poorScore)
	}
	list, _ := apiGet(t, base, "/api/vacancies?profile=1&stage=inbox")
	var order []int64
	rows, _ := list["rows"].([]any)
	for _, r := range rows {
		order = append(order, int64(r.(map[string]any)["id"].(float64)))
	}
	if want := []int64{good, poor, onsite}; fmt.Sprint(order) != fmt.Sprint(want) {
		t.Errorf("Best fit order = %v, want %v: the vacancy with a broken must last", order, want)
	}
}

func TestRequiredLanguageMissingFromMyDataCapsFitsYou(t *testing.T) {
	text := "<h2>Requirements</h2><ul><li>Mandarin</li></ul>"
	cases := []struct {
		name, me string
		capped   bool
		reason   string
	}{
		{"My data lists other languages", `{"languages":[{"name":"English","level":"native"}]}`, true, "Requires Mandarin — not in your languages"},
		{"My data lists no language", `{}`, false, "Requires Mandarin — your languages are not listed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, ids := serveWithVacancies(t, `{"name":"Tester","targetRoles":["Engineer"]}`, c.me, []mustVacancy{{"Platform Engineer", text}})
			band := bandOf(t, base, ids[0])
			score, _ := band["score"].(float64)
			if reasons := reasonsOf(band); (score <= 15) != c.capped || band["brokenMust"] != c.capped || len(reasons) == 0 || !strings.HasPrefix(reasons[0], c.reason) {
				t.Errorf("score %v, brokenMust %v, reasons %q; want capped %v with %q", score, band["brokenMust"], reasons, c.capped, c.reason)
			}
		})
	}
}
