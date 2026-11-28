package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/analysis"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type readFix struct {
	d      *db.DB
	r      *runner.Runner
	fm     *models.FakeModel
	base   string
	cat    *catalog.Catalog
	queue  func()
	logger *slog.Logger
}

func newReadFix(t *testing.T, precise bool) readFix {
	t.Helper()
	d := openTestDB(t)
	insertProfile(t, d, 1, "Backend Engineer")
	store, _, err := config.Open(filepath.Join(t.TempDir(), "sifty.yaml"))
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	if err := store.Update(func(c *config.Config) error { c.PreciseReading = &precise; return nil }); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	logger := slog.New(slog.DiscardHandler)
	r := runner.New(d, nil, time.Now, logger)
	fm := &models.FakeModel{}
	cat := catalog.New(d, time.Now)
	registerJobKinds(r, d, store, fixedModel{fm}, cat, fetchtest.NewMap(), logger, false, time.Now, nil)
	rd := &reading{database: d, store: store, runner: r}
	base := newRouteServerWithReading(t, d, r, candidate.Readers{PDF: candidate.ProgramText{Program: filepath.Join(t.TempDir(), "absent")}}, rd)
	return readFix{d: d, r: r, fm: fm, base: base, cat: cat, queue: enqueueRecomputeFit(d, r, logger, rd), logger: logger}
}

func (f readFix) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.r.Start(ctx)
}

func (f readFix) add(t *testing.T, company, intro string, published time.Time) int64 {
	t.Helper()
	url := fmt.Sprintf("https://jobs.example.com/%s/%d", strings.ToLower(company), published.Unix())
	id, _, err := f.cat.Upsert(context.Background(), listing.Posting{
		URL: url, Title: "Backend Engineer", Company: company, TextState: listing.TextFull, Published: published,
		Text: "<p>" + intro + "</p><h2>Requirements</h2><ul><li>3 years of Go</li></ul>",
	}, "src")
	if err != nil {
		t.Fatalf("upsert vacancy: %v", err)
	}
	return id
}

func (f readFix) domainTile(t *testing.T, id int64) map[string]any {
	t.Helper()
	view, status := apiGet(t, f.base, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
	if status != http.StatusOK {
		t.Fatalf("GET vacancy %d: status %d", id, status)
	}
	band, _ := view["band"].(map[string]any)
	tiles, _ := band["tiles"].([]any)
	for _, raw := range tiles {
		if tile, _ := raw.(map[string]any); tile["name"] == "Domain" {
			return tile
		}
	}
	return nil
}

func (f readFix) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.d.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func (f readFix) read() int {
	var n int
	f.d.QueryRow(`SELECT count(*) FROM fact WHERE kind='analysed' AND worker='read_domain'`).Scan(&n)
	return n
}

func (f readFix) analysed() int {
	var n int
	f.d.QueryRow(`SELECT count(*) FROM fact WHERE kind='analysed' AND worker!='read_domain'`).Scan(&n)
	return n
}

func reply(domain, quote string) models.Response {
	return models.Response{Text: fmt.Sprintf(`{"domains":[{"domain":%q,"quote":%q}]}`, domain, quote)}
}

func TestPreciseReadingReadsNewestCompanyFirstAndEachCompanyOnce(t *testing.T) {
	f := newReadFix(t, true)
	day := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	const stackworks = "Stackworks runs a crypto exchange for retail traders."
	const orbitlab = "Orbitlab builds payment rails for online merchants."
	const northwind = "Northwind makes smart contract tooling for DeFi protocols."
	older := f.add(t, "Stackworks", stackworks, day)
	north := f.add(t, "Northwind", northwind, day.Add(24*time.Hour))
	orbit := f.add(t, "Orbitlab", orbitlab, day.Add(48*time.Hour))
	newer := f.add(t, "Stackworks", stackworks, day.Add(72*time.Hour))
	f.fm.AddReply(reply("exchange", stackworks))
	f.fm.AddReply(reply("payments", orbitlab))
	f.fm.AddReply(reply("defi", northwind))

	f.queue()
	f.start(t)
	waitFor(t, 10*time.Second, func() bool { return f.read() == 4 })

	calls := f.fm.Calls()
	if len(calls) != 3 {
		t.Fatalf("model calls = %d, want 3 (one per company)", len(calls))
	}
	for i, want := range []string{stackworks, orbitlab, northwind} {
		if !strings.Contains(calls[i].Prompt, want) {
			t.Errorf("call %d does not read %q; companies are not read newest first", i+1, want)
		}
	}
	for id, want := range map[int64]string{older: "Exchange", newer: "Exchange", orbit: "Payments", north: "DeFi"} {
		tile := f.domainTile(t, id)
		if tile == nil || tile["value"] != want || tile["line"] != "Read by the model" || tile["verdict"] != "–" {
			t.Errorf("vacancy %d Domain tile = %v, want %s read by the model", id, tile, want)
		}
	}
	fresh := f.add(t, "Stackworks", stackworks, day.Add(96*time.Hour))
	f.queue()
	waitFor(t, 10*time.Second, func() bool { return f.read() == 5 })
	if tile := f.domainTile(t, fresh); tile == nil || tile["value"] != "Exchange" {
		t.Errorf("new vacancy of a read company: Domain tile = %v, want Exchange", tile)
	}
	if n := len(f.fm.Calls()); n != 3 {
		t.Errorf("model calls after a new vacancy of a read company = %d, want 3", n)
	}
}

func TestOpeningVacancyReadsItsCompanyWhenPreciseReadingIsOff(t *testing.T) {
	f := newReadFix(t, false)
	day := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	const stackworks = "Stackworks runs a crypto exchange for retail traders."
	opened := f.add(t, "Stackworks", stackworks, day)
	f.add(t, "Northwind", "Northwind makes smart contract tooling for DeFi protocols.", day.Add(time.Hour))
	f.fm.AddReply(reply("exchange", stackworks))

	f.queue()
	f.start(t)
	waitFor(t, 10*time.Second, func() bool { return f.analysed() == 2*len(analysis.Workers) })
	time.Sleep(300 * time.Millisecond)
	if n := len(f.fm.Calls()); n != 0 {
		t.Fatalf("model calls with Precise reading off = %d, want 0", n)
	}
	f.domainTile(t, opened)
	waitFor(t, 10*time.Second, func() bool { return f.read() == 1 })
	if tile := f.domainTile(t, opened); tile == nil || tile["value"] != "Exchange" {
		t.Fatalf("opened vacancy Domain tile = %v, want Exchange", tile)
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(f.fm.Calls()); n != 1 {
		t.Errorf("model calls after opening one vacancy twice = %d, want 1", n)
	}
}

func TestDomainAnswerWithQuoteNotInTextGivesNoFact(t *testing.T) {
	f := newReadFix(t, true)
	id := f.add(t, "Stackworks", "Stackworks runs a crypto exchange for retail traders.", time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	f.fm.AddReply(reply("gambling", "Stackworks runs the largest casino in the world."))
	f.fm.AddReply(reply("gambling", "Stackworks runs the largest casino in the world."))

	f.queue()
	f.start(t)
	waitFor(t, 10*time.Second, func() bool {
		return f.count(t, `SELECT count(*) FROM job WHERE kind='read_model' AND state='done'`) == 1
	})

	if tile := f.domainTile(t, id); tile != nil {
		t.Errorf("Domain tile = %v from a quote that is not in the text, want none", tile)
	}
	if n := f.count(t, `SELECT count(*) FROM fact WHERE kind='domain'`); n != 0 {
		t.Errorf("domain facts = %d, want 0", n)
	}
	calls := len(f.fm.Calls())
	f.queue()
	time.Sleep(300 * time.Millisecond)
	if n := f.count(t, `SELECT count(*) FROM job WHERE kind='read_model'`); n != 1 || len(f.fm.Calls()) != calls {
		t.Errorf("company asked again after an unusable answer: jobs = %d, calls = %d (was %d)", n, len(f.fm.Calls()), calls)
	}
}

func TestNewDomainAnswerGivesNewFitsYouResult(t *testing.T) {
	f := newReadFix(t, false)
	const stackworks = "Stackworks runs a crypto exchange for retail traders."
	id := f.add(t, "Stackworks", stackworks, time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	f.fm.AddReply(reply("exchange", stackworks))
	listingHash := func() (h int64) {
		f.d.QueryRow(`SELECT listing FROM fit WHERE vacancy=?`, id).Scan(&h)
		return h
	}

	f.queue()
	f.start(t)
	waitFor(t, 10*time.Second, func() bool { return f.analysed() == len(analysis.Workers) && listingHash() != 0 })
	before := listingHash()

	f.domainTile(t, id)
	waitFor(t, 10*time.Second, func() bool { return f.read() == 1 })
	waitFor(t, 10*time.Second, func() bool {
		return listingHash() != before && f.count(t, `SELECT count(*) FROM job WHERE kind='recompute_fit' AND state IN ('queued','running')`) == 0
	})
}

func TestVacancyReadByTheModelBeforeAnalysisIsStillNotAnalysed(t *testing.T) {
	f := newReadFix(t, false)
	const stackworks = "Stackworks runs a crypto exchange for retail traders."
	id := f.add(t, "Stackworks", stackworks, time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	f.fm.AddReply(reply("exchange", stackworks))

	f.domainTile(t, id)
	f.start(t)
	waitFor(t, 10*time.Second, func() bool { return f.read() == 1 })

	view, _ := apiGet(t, f.base, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
	band, _ := view["band"].(map[string]any)
	if text := band["requirementsText"]; text != "Not analysed yet" {
		t.Errorf("requirements text of a vacancy only the model read = %v, want Not analysed yet", text)
	}
	if f.analysed() != 0 {
		t.Errorf("code analysis ran without being queued")
	}
}

func TestPaidProviderWithoutPermissionQueuesNoBulkReadingAndTheOpenedReadWaitsWithoutRequest(t *testing.T) {
	var requests atomic.Int64
	paid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(paid.Close)

	dir := t.TempDir()
	cfg := fmt.Sprintf(`providers:
  paidbox: {type: openai, model: gpt-turbo, cost: paid, endpoint: %q}
work: {reading: paidbox, shortlist_check: paidbox, preparation: paidbox}
precise_reading: true
`, paid.URL)
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatalf("write sifty.yaml: %v", err)
	}
	seed, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	insertProfile(t, seed, 1, "Backend Engineer")
	opened := insertFullVacancy(t, seed, "https://jobs.example.com/paid-1", "Backend Engineer", "Stackworks")
	insertFullVacancy(t, seed, "https://jobs.example.com/paid-2", "Backend Engineer", "Northwind")
	seed.Close()

	port, cancel := startServer(t, dir, false)
	defer cancel()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	time.Sleep(500 * time.Millisecond)
	if work, _ := apiGet(t, base, "/api/work"); len(work["waiting"].([]any)) != 0 {
		t.Fatalf("bulk reading queued on a paid provider without permission: %v", work["waiting"])
	}
	if _, status := apiGet(t, base, fmt.Sprintf("/api/vacancies/%d?profile=1", opened)); status != http.StatusOK {
		t.Fatalf("GET vacancy: status %d", status)
	}
	waitFor(t, 10*time.Second, func() bool {
		view, _ := apiGet(t, base, "/api/work")
		waiting, _ := view["waiting"].([]any)
		for _, raw := range waiting {
			if w, _ := raw.(map[string]any); w["kind"] == "read_model" && w["count"] == float64(1) {
				reason, _ := w["reason"].(map[string]any)
				return reason != nil && reason["code"] == "paid"
			}
		}
		return false
	})
	time.Sleep(300 * time.Millisecond)
	if n := requests.Load(); n != 0 {
		t.Errorf("paid provider received %d requests, want 0", n)
	}
}
