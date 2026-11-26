package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
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
	r.Enqueue(runner.Spec{Kind: "recompute_fit"})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.Start(runCtx)

	waitFor(t, 5*time.Second, func() bool {
		var n int
		d.QueryRow(`SELECT count(*) FROM fact WHERE vacancy=? AND kind=?`, full, facts.Analysed).Scan(&n)
		return n == 3
	})

	got, err := facts.Load(ctx, d, full, before.version)
	if err != nil {
		t.Fatalf("load facts: %v", err)
	}
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
