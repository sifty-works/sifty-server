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
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/skills"
)

func TestAnalysisSavesFactsWithoutModelCall(t *testing.T) {
	d := openTestDB(t)
	insertProfile(t, d, 1, "Platform Engineer")
	cat := catalog.New(d, time.Now)
	ctx := context.Background()
	full, _, err := cat.Upsert(ctx, listing.Posting{
		URL: "https://jobs.example.com/an1", Title: "Platform Engineer", Company: "Stackworks", TextState: listing.TextFull,
		Text: "<p>Fully remote. Salary: $100k - $120k per year.</p><h2>Requirements</h2><ul><li>5 years of Go</li></ul>",
	}, "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	summary, _, err := cat.Upsert(ctx, listing.Posting{
		URL: "https://jobs.example.com/an2", Title: "Platform Engineer", Company: "Northwind", TextState: listing.TextSummary,
		Text: "<p>Short teaser.</p>",
	}, "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	type row struct {
		body    string
		version int64
		hash    string
	}
	stored := func() (r row) {
		d.QueryRow(`SELECT hex(body), version, content_hash FROM vacancy WHERE id=?`, full).Scan(&r.body, &r.version, &r.hash)
		return r
	}
	before := stored()

	fm := &models.FakeModel{}
	r := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r, d, fm)
	enqueueRecomputeFit(d, r, slog.Default(), nil)()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.Start(runCtx)

	waitFor(t, 5*time.Second, func() bool {
		var n int
		d.QueryRow(`SELECT count(*) FROM fact WHERE vacancy=? AND kind=?`, full, facts.Analysed).Scan(&n)
		return n == len(analysis.Workers)
	})

	sets, err := facts.Load(ctx, d, full)
	if err != nil {
		t.Fatalf("load facts: %v", err)
	}
	got := sets[full].Facts
	var money, requirement bool
	for _, f := range got {
		money = money || f.Kind == facts.Money && f.Flag == ""
		requirement = requirement || f.Kind == facts.Requirement && f.Flag == facts.Required && f.Value == "5 years of Go"
	}
	if !money || !requirement {
		t.Errorf("facts = %+v, want money and the required line", got)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM fact WHERE vacancy=?`, summary).Scan(&n)
	if n != 0 {
		t.Errorf("summary-only vacancy has %d facts, want none", n)
	}
	if calls := len(fm.Calls()); calls != 0 {
		t.Errorf("model calls = %d, want 0", calls)
	}
	if after := stored(); after != before {
		t.Errorf("stored vacancy changed by analysis: %+v -> %+v", before, after)
	}
}

func TestAnalysisRunsBeforeFitsYouAndFitsYouReadsTheFacts(t *testing.T) {
	var requests atomic.Int64
	prov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(prov.Close)
	dir := t.TempDir()
	cfg := fmt.Sprintf("providers:\n  box: {type: openai, model: m, cost: local, endpoint: %q}\nwork: {reading: box, shortlist_check: box, preparation: box}\nprecise_reading: false\n", prov.URL)
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	seed, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := seed.Exec(`UPDATE profile SET data=? WHERE id=1`,
		`{"name":"Tester","targetRoles":["Platform Engineer"],"compensation":{"cashMin":50000,"currency":"EUR","period":"year"}}`); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(seed, time.Now)
	add := func(url, title, text string) int64 {
		id, _, err := cat.Upsert(context.Background(), listing.Posting{URL: url, Title: title, Company: "Stackworks", TextState: listing.TextFull, Text: text}, "src")
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		return id
	}
	visible := add("https://jobs.example.com/an-v", "Platform Engineer", "<p>Salary: EUR 30,000 per year. Fully remote.</p><h2>Requirements</h2><ul><li>Go</li></ul>")
	hidden := add("https://jobs.example.com/an-h", "Pastry Chef", "<p>Salary: EUR 30,000 per year.</p>")
	seed.Close()

	_, cancel := startServer(t, dir, false)
	defer cancel()
	probe, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	defer probe.Close()
	waitFor(t, 10*time.Second, func() bool {
		var scored, active int
		probe.QueryRow(`SELECT count(*) FROM fit WHERE vacancy=? AND state='scored'`, visible).Scan(&scored)
		probe.QueryRow(`SELECT count(*) FROM job WHERE state IN ('queued','running')`).Scan(&active)
		return scored == 1 && active == 0
	})

	var recomputes int
	probe.QueryRow(`SELECT count(*) FROM job WHERE kind='recompute_fit'`).Scan(&recomputes)
	if recomputes != 1 {
		t.Errorf("recompute_fit jobs = %d, want 1: Fit was recomputed before the analysis ended or once per analysis", recomputes)
	}
	var score int
	probe.QueryRow(`SELECT score FROM fit WHERE profile=1 AND vacancy=?`, visible).Scan(&score)
	p, err := fit.LoadProfile(context.Background(), probe, 1, fit.Rules{Weights: config.Defaults().Weights, Rates: config.Defaults().Rates})
	if err != nil {
		t.Fatal(err)
	}
	rules := fit.Rules{Weights: config.Defaults().Weights, Rates: config.Defaults().Rates}
	sets, err := facts.Load(context.Background(), probe, visible, hidden)
	if err != nil {
		t.Fatal(err)
	}
	fromFacts := fit.Score(p.View, fit.FromFacts("Platform Engineer", true, sets[visible]), rules).Score
	fromNothing := fit.Score(p.View, fit.Vacancy{Title: "Platform Engineer", Full: true}, rules).Score
	if score != fromFacts || score == fromNothing {
		t.Errorf("Fit = %d, want %d from the facts and not %d from none", score, fromFacts, fromNothing)
	}
	if len(sets[hidden].Facts) != 0 || sets[hidden].Analysed {
		t.Errorf("a vacancy hidden in every profile was analysed: %+v", sets[hidden])
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the provider received %d requests, want 0", n)
	}
}

func TestAnUnreadableProfileDoesNotStopAnalysisOfTheOthersAndIsReported(t *testing.T) {
	d := openTestDB(t)
	insertProfile(t, d, 1, "Platform Engineer")
	id := insertFullVacancy(t, d, "https://jobs.example.com/pe9", "Platform Engineer", "Stackworks")
	if _, err := d.Exec(`INSERT INTO profile (id, data) VALUES (2, '{"targetRoles": 17')`); err != nil {
		t.Fatalf("insert broken profile: %v", err)
	}
	r := runner.New(d, nil, time.Now, slog.Default())
	err := queueAnalysis(context.Background(), d, r)
	if err == nil || !strings.Contains(err.Error(), "profile 2") {
		t.Errorf("error = %v, want it to name profile 2", err)
	}
	if jobs := r.Active(analysis.Kind); len(jobs) != 1 || jobs[0].Subject != analysis.Subject(id, 1) {
		t.Errorf("queued %+v, want the analysis of the vacancy visible in the readable profile", jobs)
	}
}

func TestVacancyIsAnalysedAgainWhenTheDataTheWorkersReadChanges(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	id, _, err := catalog.New(d, time.Now).Upsert(ctx, listing.Posting{
		URL: "https://jobs.example.com/dv1", Title: "Platform Engineer", Company: "Stackworks", TextState: listing.TextFull,
		Text: "<p>Fully remote.</p><h2>Requirements</h2><ul><li>5 years of Go</li></ul>",
	}, "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	pending := func() int {
		got, err := analysis.Pending(ctx, d, "1=1", nil)
		if err != nil {
			t.Fatalf("pending: %v", err)
		}
		return len(got)
	}
	if pending() != 1 {
		t.Fatalf("pending before analysis = %d, want 1", pending())
	}
	if err := analysis.Analyse(ctx, d, loadForAnalysis(d), id, 1); err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if n := pending(); n != 0 {
		t.Fatalf("pending after analysis = %d, want 0", n)
	}

	original := skills.VocabBytes
	t.Cleanup(func() { skills.VocabBytes = original })
	skills.VocabBytes = append(append([]byte{}, original...), "\n# edited\n"...)
	if n := pending(); n != 1 {
		t.Errorf("pending after the skills vocabulary changed = %d, want 1", n)
	}
	skills.VocabBytes = original
	if n := pending(); n != 0 {
		t.Errorf("pending after the vocabulary was restored = %d, want 0", n)
	}
}
