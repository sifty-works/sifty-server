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
	d, cat, ids := fixture(t)
	recompute(t, d, nil)
	for _, pid := range []int64{1, 2} {
		p, err := fit.LoadProfile(ctx, d, pid, rules())
		if err != nil {
			t.Fatal(err)
		}
		for _, vid := range ids {
			_, v, _, _, _ := fit.LoadVacancy(ctx, cat, vid)
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

func insertReading(t *testing.T, d *db.DB, vacancyID, version int64, readerVersion, record, created string) {
	t.Helper()
	_, err := d.Exec(
		`INSERT OR REPLACE INTO vacancy_reading (vacancy_id, version, reader_version, record, created) VALUES (?,?,?,?,?)`,
		vacancyID, version, readerVersion, record, created)
	if err != nil {
		t.Fatalf("insert reading: %v", err)
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

func TestReadingMoneyFillsWhereCodeFoundNone(t *testing.T) {
	d, cat, _ := fixture(t)
	d.Exec(`UPDATE profile SET data='` + compensationProfile + `' WHERE id=1`)
	id := noSalaryVacancy(t, cat)
	insertReading(t, d, id, 1, "import-1",
		`{"money":{"value":"EUR 80,000 per year","quote":"Salary: EUR 80,000 per year"}}`,
		"2024-01-01T00:00:00Z")
	recompute(t, d, nil)
	withReading := stored(t, d, 1, id)
	d.Exec(`DELETE FROM vacancy_reading WHERE vacancy_id=?`, id)
	recompute(t, d, nil)
	withoutReading := stored(t, d, 1, id)
	if withReading.score.Int64 <= withoutReading.score.Int64 {
		t.Errorf("reading money not used: with=%d without=%d", withReading.score.Int64, withoutReading.score.Int64)
	}
}

func TestCodeMoneyWinsOverReadingMoney(t *testing.T) {
	d, _, ids := fixture(t)
	d.Exec(`UPDATE profile SET data='` + compensationProfile + `' WHERE id=1`)
	recompute(t, d, nil)
	codeOnly := stored(t, d, 1, ids[0])
	insertReading(t, d, ids[0], 1, "import-1",
		`{"money":{"value":"EUR 20,000 per year","quote":"Salary: EUR 20,000 per year"}}`,
		"2024-01-01T00:00:00Z")
	recompute(t, d, nil)
	withReading := stored(t, d, 1, ids[0])
	if withReading.score.Int64 != codeOnly.score.Int64 {
		t.Errorf("code money overridden by reading: before=%d after=%d", codeOnly.score.Int64, withReading.score.Int64)
	}
}

func TestReadingOfOlderVersionIsIgnored(t *testing.T) {
	d, cat, _ := fixture(t)
	d.Exec(`UPDATE profile SET data='` + compensationProfile + `' WHERE id=1`)
	id := noSalaryVacancy(t, cat)
	cat.Upsert(ctx, listing.Posting{URL: "https://jobs.example.org/10", Title: "Backend Engineer", Company: "Initech",
		Text: "<p>Updated role description.</p>", TextState: listing.TextFull}, "fixture")
	insertReading(t, d, id, 1, "import-1",
		`{"money":{"value":"EUR 80,000 per year","quote":"Salary: EUR 80,000 per year"}}`,
		"2024-01-01T00:00:00Z")
	recompute(t, d, nil)
	got := stored(t, d, 1, id)
	if got.version != 2 {
		t.Fatalf("vacancy should be at version 2, got %d", got.version)
	}
	d.Exec(`DELETE FROM vacancy_reading WHERE vacancy_id=?`, id)
	recompute(t, d, nil)
	noReading := stored(t, d, 1, id)
	if got.score.Int64 != noReading.score.Int64 {
		t.Errorf("reading for version 1 used for version 2: with-reading=%d without-reading=%d",
			got.score.Int64, noReading.score.Int64)
	}
}

func TestStoringReadingMakesStoredFitStale(t *testing.T) {
	d, cat, _ := fixture(t)
	d.Exec(`UPDATE profile SET data='` + compensationProfile + `' WHERE id=1`)
	id := noSalaryVacancy(t, cat)
	recompute(t, d, nil)
	before := stored(t, d, 1, id)
	insertReading(t, d, id, 1, "import-1",
		`{"money":{"value":"EUR 80,000 per year","quote":"Salary: EUR 80,000 per year"}}`,
		"2024-06-01T00:00:00Z")
	recompute(t, d, nil)
	after := stored(t, d, 1, id)
	if before.score.Int64 == after.score.Int64 {
		t.Errorf("fit not recomputed after reading stored: before=%d after=%d", before.score.Int64, after.score.Int64)
	}
}

func TestReadingWithNoRequirementsStoresZeroReqCounts(t *testing.T) {
	d, cat, _ := fixture(t)
	id := noSalaryVacancy(t, cat)
	insertReading(t, d, id, 1, "test-reader", `{"requirements":[]}`, "2026-01-01T00:00:00Z")
	recompute(t, d, nil)
	var reqOK, reqNo, reqMaybe sql.NullInt64
	d.QueryRow(`SELECT req_ok, req_no, req_maybe FROM fit WHERE profile=1 AND vacancy=?`, id).
		Scan(&reqOK, &reqNo, &reqMaybe)
	if !reqOK.Valid || !reqNo.Valid || !reqMaybe.Valid {
		t.Errorf("want req counts not null, got req_ok=%v req_no=%v req_maybe=%v", reqOK, reqNo, reqMaybe)
	}
	if reqOK.Int64 != 0 || reqNo.Int64 != 0 || reqMaybe.Int64 != 0 {
		t.Errorf("want 0,0,0 got %d,%d,%d", reqOK.Int64, reqNo.Int64, reqMaybe.Int64)
	}
}

func TestDeletingVacancyRemovesReading(t *testing.T) {
	d, cat, ids := fixture(t)
	insertReading(t, d, ids[0], 1, "import-1", `{}`, "2024-01-01T00:00:00Z")
	var n int
	d.QueryRow(`SELECT count(*) FROM vacancy_reading WHERE vacancy_id=?`, ids[0]).Scan(&n)
	if n != 1 {
		t.Fatalf("reading not inserted: count=%d", n)
	}
	cat.Delete(ctx, ids[0])
	d.QueryRow(`SELECT count(*) FROM vacancy_reading WHERE vacancy_id=?`, ids[0]).Scan(&n)
	if n != 0 {
		t.Errorf("reading not removed after vacancy deleted: count=%d", n)
	}
}

func TestReadingReplacedUnderSameReaderVersionRecomputesFit(t *testing.T) {
	d, cat, _ := fixture(t)
	d.Exec(`UPDATE profile SET data='` + compensationProfile + `' WHERE id=1`)
	id := noSalaryVacancy(t, cat)
	insertReading(t, d, id, 1, "import-1",
		`{"money":{"value":"EUR 60,000 per year","quote":"Salary: EUR 60,000 per year"}}`,
		"2024-01-01T00:00:00Z")
	recompute(t, d, nil)
	first := stored(t, d, 1, id)
	insertReading(t, d, id, 1, "import-1",
		`{"money":{"value":"EUR 20,000 per year","quote":"Salary: EUR 20,000 per year"}}`,
		"2024-06-01T00:00:00Z")
	recompute(t, d, nil)
	second := stored(t, d, 1, id)
	if first.score.Int64 == second.score.Int64 {
		t.Errorf("fit not recomputed after reading replaced: first=%d second=%d", first.score.Int64, second.score.Int64)
	}
}
