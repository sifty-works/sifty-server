package fit_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/listing"
)

var ctx = context.Background()

func fixture(t *testing.T) (*db.DB, *catalog.Catalog, []int64) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "fit.db"), db.Migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.Exec(`UPDATE profile SET data='{"name":"One","targetRoles":["Backend Engineer"]}' WHERE id=1`)
	d.Exec(`INSERT INTO profile (data) VALUES ('{"name":"Two","targetRoles":["Data Analyst"]}')`)
	cat := catalog.New(d, func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) })
	var ids []int64
	for i, p := range []listing.Posting{
		{URL: "https://jobs.example.org/1", Title: "Backend Engineer", Company: "Acme", Text: "<p>Salary: EUR 90,000 per year. Fully remote.</p>", TextState: listing.TextFull},
		{URL: "https://jobs.example.org/2", Title: "Data Analyst", Company: "Globex", Text: "<p>Short note</p>", TextState: listing.TextSummary},
	} {
		id, _, err := cat.Upsert(ctx, p, "fixture")
		if err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	return d, cat, ids
}

func recompute(t *testing.T, d *db.DB, progress func(int, int, string)) {
	t.Helper()
	if progress == nil {
		progress = func(int, int, string) {}
	}
	if err := fit.Recompute(ctx, d, rules, progress); err != nil {
		t.Fatalf("recompute: %v", err)
	}
}

type row struct {
	version, inputs int64
	state           string
	score           sql.NullInt64
}

func stored(t *testing.T, d *db.DB, profile, vacancy int64) row {
	t.Helper()
	var r row
	if err := d.QueryRow(`SELECT version, inputs, state, score FROM fit WHERE profile=? AND vacancy=?`, profile, vacancy).
		Scan(&r.version, &r.inputs, &r.state, &r.score); err != nil {
		t.Fatalf("fit row %d/%d: %v", profile, vacancy, err)
	}
	return r
}

func TestRecomputeStoresTheResultOfScoreForEveryProfileAndVacancy(t *testing.T) {
	d, _, ids := fixture(t)
	recompute(t, d, nil)
	for _, pid := range []int64{1, 2} {
		p, err := fit.LoadProfile(ctx, d, pid, rules())
		if err != nil {
			t.Fatal(err)
		}
		for _, vid := range ids {
			v, _, _, _, _ := fit.LoadVacancy(ctx, d, vid)
			want := fit.Score(p.View, v, rules())
			got := stored(t, d, pid, vid)
			if got.inputs != p.Inputs || got.version != 1 || got.state != want.State || (want.State == fit.Scored) != got.score.Valid || int(got.score.Int64) != want.Score {
				t.Errorf("profile %d vacancy %d: stored %+v, want %s %d", pid, vid, got, want.State, want.Score)
			}
		}
	}
	if got := stored(t, d, 1, ids[1]); got.state != fit.NoText {
		t.Errorf("vacancy without full text: state %q", got.state)
	}
}

func TestRecomputeOnlyRecomputesResultsThatAreOutOfDate(t *testing.T) {
	d, cat, ids := fixture(t)
	recompute(t, d, nil)
	d.Exec(`UPDATE fit SET score=7 WHERE profile=1`)
	d.Exec(`UPDATE profile SET data='{"name":"Two","targetRoles":["Backend Engineer"]}' WHERE id=2`)
	cat.Upsert(ctx, listing.Posting{URL: "https://jobs.example.org/3", Title: "Backend Engineer", Company: "Initech",
		Text: "<p>Remote role</p>", TextState: listing.TextFull}, "fixture")
	cat.Upsert(ctx, listing.Posting{URL: "https://jobs.example.org/2", Title: "Data Analyst", Company: "Globex",
		Text: "<p>A longer note</p>", TextState: listing.TextFull}, "fixture")
	recompute(t, d, nil)
	if got := stored(t, d, 1, ids[1]); got.version != 2 || got.state != fit.Scored {
		t.Errorf("result of the changed vacancy not recomputed: %+v", got)
	}
	if got := stored(t, d, 1, ids[0]); got.score.Int64 != 7 {
		t.Errorf("a current result was recomputed: %+v", got)
	}
	p2, _ := fit.LoadProfile(ctx, d, 2, rules())
	if got := stored(t, d, 2, ids[0]); got.inputs != p2.Inputs {
		t.Errorf("result of the changed profile not recomputed: %+v", got)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM fit`).Scan(&n)
	if n != 6 {
		t.Errorf("rows after a new vacancy: %d, want 6", n)
	}
}

func TestRecomputeWritesEveryChangedVacancyAndReadsNoBatchPastTheLast(t *testing.T) {
	d, cat, ids := fixture(t)
	for i := 3; i <= 650; i++ {
		_, _, err := cat.Upsert(ctx, listing.Posting{URL: fmt.Sprintf("https://jobs.example.org/%d", i), Title: "Backend Engineer",
			Company: fmt.Sprintf("Company %d", i), Text: "<p>Remote role</p>", TextState: listing.TextFull}, "fixture")
		if err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	recompute(t, d, nil)

	late := int64(600)
	for _, n := range []int{1, 600} {
		if _, _, err := cat.Upsert(ctx, listing.Posting{URL: fmt.Sprintf("https://jobs.example.org/%d", n), Title: "Backend Engineer",
			Company: "Acme", Text: "<p>Salary: EUR 95,000 per year. Fully remote.</p>", TextState: listing.TextFull}, "fixture"); err != nil {
			t.Fatalf("change vacancy %d: %v", n, err)
		}
	}
	var calls []int
	recompute(t, d, func(written, total int, _ string) { calls = append(calls, total) })

	if len(calls) != 3 || calls[0] != 4 {
		t.Errorf("progress calls with totals %v, want three calls with total 4: batches 1 to 3 hold the changed vacancies and batch 4 is not read", calls)
	}
	for _, pid := range []int64{1, 2} {
		for _, vid := range []int64{ids[0], late} {
			if got := stored(t, d, pid, vid); got.version != 2 {
				t.Errorf("profile %d vacancy %d: result of the changed vacancy not recomputed: %+v", pid, vid, got)
			}
		}
	}
}

func TestProfileChangedWhileRecomputingIsApplied(t *testing.T) {
	d, _, ids := fixture(t)
	changed := false
	recompute(t, d, func(int, int, string) {
		if !changed {
			changed = true
			d.Exec(`UPDATE profile SET data='{"name":"One","targetRoles":["Designer"]}' WHERE id=1`)
		}
	})
	p, _ := fit.LoadProfile(ctx, d, 1, rules())
	if got := stored(t, d, 1, ids[0]); got.inputs != p.Inputs {
		t.Errorf("the change made during the run was not applied: %+v", got)
	}
}

func TestListingChangeWithoutTextChangeIsRecomputed(t *testing.T) {
	d, cat, ids := fixture(t)
	recompute(t, d, nil)
	d.Exec(`UPDATE fit SET score=7 WHERE profile=1`)
	cat.Upsert(ctx, listing.Posting{URL: "https://jobs.example.org/1", Title: "Backend Engineer", Company: "Acme", Location: "Berlin, Germany",
		Text: "<p>Salary: EUR 90,000 per year. Fully remote.</p>", TextState: listing.TextFull}, "fixture")
	recompute(t, d, nil)
	if got := stored(t, d, 1, ids[0]); got.version != 1 || got.score.Int64 == 7 {
		t.Errorf("a location change without a text change was not recomputed: %+v", got)
	}
}

func saveMoney(t *testing.T, d *db.DB, id, version int64, amount int) {
	t.Helper()
	money := fmt.Sprintf(`{"max":%d,"currency":"EUR","period":"year"}`, amount)
	found := []facts.Fact{
		{Kind: facts.Money, Value: money, Source: facts.Source{Way: facts.SiteField}},
		{Kind: facts.Analysed, Value: money, Source: facts.Source{Way: facts.Vocabulary}},
	}
	if err := facts.Save(ctx, d, id, version, "site_fields", found); err != nil {
		t.Fatalf("save facts: %v", err)
	}
}

func noSalaryVacancy(t *testing.T, cat *catalog.Catalog) int64 {
	t.Helper()
	id, _, err := cat.Upsert(ctx, listing.Posting{
		URL:       "https://jobs.example.org/10",
		Title:     "Backend Engineer",
		Company:   "Initech",
		Text:      "<p>Senior role. Great team.</p>",
		TextState: listing.TextFull,
	}, "fixture")
	if err != nil {
		t.Fatalf("upsert no-salary vacancy: %v", err)
	}
	return id
}

const compensationProfile = `{"name":"One","targetRoles":["Backend Engineer"],"compensation":{"cashMin":50000,"currency":"EUR","period":"year"}}`

func TestStoredFitFollowsTheFactsOfTheCurrentVersion(t *testing.T) {
	t.Run("a money fact moves the number", func(t *testing.T) {
		d, cat, _ := fixture(t)
		d.Exec(`UPDATE profile SET data='` + compensationProfile + `' WHERE id=1`)
		id := noSalaryVacancy(t, cat)
		recompute(t, d, nil)
		before := stored(t, d, 1, id)
		saveMoney(t, d, id, 1, 20000)
		recompute(t, d, nil)
		if after := stored(t, d, 1, id); after.score.Int64 >= before.score.Int64 {
			t.Errorf("money fact not used: before=%d after=%d", before.score.Int64, after.score.Int64)
		}
	})
	t.Run("replacing the facts of the same version recomputes the result", func(t *testing.T) {
		d, cat, _ := fixture(t)
		d.Exec(`UPDATE profile SET data='` + compensationProfile + `' WHERE id=1`)
		id := noSalaryVacancy(t, cat)
		saveMoney(t, d, id, 1, 80000)
		recompute(t, d, nil)
		first := stored(t, d, 1, id)
		saveMoney(t, d, id, 1, 20000)
		recompute(t, d, nil)
		if second := stored(t, d, 1, id); second.score.Int64 >= first.score.Int64 {
			t.Errorf("result not recomputed after the facts were replaced: first=%d second=%d", first.score.Int64, second.score.Int64)
		}
	})
	t.Run("facts of an older version are ignored", func(t *testing.T) {
		d, cat, _ := fixture(t)
		d.Exec(`UPDATE profile SET data='` + compensationProfile + `' WHERE id=1`)
		id := noSalaryVacancy(t, cat)
		saveMoney(t, d, id, 1, 80000)
		cat.Upsert(ctx, listing.Posting{URL: "https://jobs.example.org/10", Title: "Backend Engineer", Company: "Initech",
			Text: "<p>Updated role description.</p>", TextState: listing.TextFull}, "fixture")
		recompute(t, d, nil)
		got := stored(t, d, 1, id)
		if got.version != 2 {
			t.Fatalf("vacancy should be at version 2, got %d", got.version)
		}
		p, _ := fit.LoadProfile(ctx, d, 1, rules())
		if want := fit.Score(p.View, fit.Vacancy{Title: "Backend Engineer", Full: true}, rules()).Score; int(got.score.Int64) != want {
			t.Errorf("facts of version 1 used for version 2: score %d, want %d", got.score.Int64, want)
		}
	})
}

func TestRecomputeMarksOnlyAnalysedVacanciesAnalysed(t *testing.T) {
	d, cat, _ := fixture(t)
	analysed := noSalaryVacancy(t, cat)
	saveMoney(t, d, analysed, 1, 80000)
	recompute(t, d, nil)
	flag := func(id int64) (n sql.NullBool) {
		d.QueryRow(`SELECT analysed FROM fit WHERE profile=1 AND vacancy=?`, id).Scan(&n)
		return n
	}
	if n := flag(analysed); !n.Bool {
		t.Errorf("analysed vacancy without requirement lines: analysed %v, want true", n)
	}
	if n := flag(1); n.Bool {
		t.Errorf("vacancy not analysed: analysed %v, want false", n)
	}
}
