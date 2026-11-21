package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

var retentionBase = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

func expiryPosting(url string, expires time.Time) listing.Posting {
	return listing.Posting{
		URL:       url,
		Title:     "Data Engineer",
		Company:   "RetentionCo",
		Text:      "Job description.",
		TextState: listing.TextFull,
		Expires:   expires,
	}
}

func runRetentionJob(t *testing.T, d *db.DB, cat *catalog.Catalog, now time.Time) runner.Job {
	t.Helper()
	r := runner.New(d, nil, func() time.Time { return now }, slog.Default())
	registerKindsAt(t, r, d, &models.FakeModel{}, cat, func() time.Time { return now })
	r.Enqueue(runner.Spec{Kind: "retention"})
	rCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(rCtx)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := r.Last("retention"); ok && j.State == "done" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("retention job did not complete")
	return runner.Job{}
}

func TestRetentionExpiredVacanciesWithoutApplicationRemoved(t *testing.T) {
	d := openTestDB(t)
	now := retentionBase
	twoDaysAgo := now.Add(-48 * time.Hour)
	fewHoursAgo := now.Add(-6 * time.Hour)

	cat := catalog.New(d, func() time.Time { return now })
	ctx := context.Background()

	id1, _, _ := cat.Upsert(ctx, expiryPosting("https://example.com/jobs/e1", twoDaysAgo), "src")
	id2, _, _ := cat.Upsert(ctx, expiryPosting("https://example.com/jobs/e2", twoDaysAgo), "src")
	d.Exec(`INSERT INTO application (vacancy,applied_at,profile,profile_name) VALUES (?,?,1,'Tester')`,
		id2, now.Format(time.RFC3339))
	id3, _, _ := cat.Upsert(ctx, expiryPosting("https://example.com/jobs/e3", fewHoursAgo), "src")
	id4, _, _ := cat.Upsert(ctx, listing.Posting{
		URL: "https://example.com/jobs/e4", Title: "Analyst", Company: "RetentionCo",
		TextState: listing.TextFull,
	}, "src")
	id5, _, _ := cat.Upsert(ctx, expiryPosting("https://example.com/jobs/e5", now.Add(7*24*time.Hour)), "src")
	d.Exec(`INSERT INTO profile (id,data) VALUES (1,'{"name":"P1"}') ON CONFLICT DO NOTHING`)
	id6, _, _ := cat.Upsert(ctx, expiryPosting("https://example.com/jobs/e6", twoDaysAgo), "src")
	d.Exec(`INSERT OR REPLACE INTO decision (profile,vacancy,stage,reason,decided_at) VALUES (1,?,'shortlist','',?)`,
		id6, now.Format(time.RFC3339))
	id7, _, _ := cat.Upsert(ctx, expiryPosting("https://example.com/jobs/e7", twoDaysAgo), "src")
	d.Exec(`INSERT OR REPLACE INTO decision (profile,vacancy,stage,reason,decided_at) VALUES (1,?,'dismissed','',?)`,
		id7, now.Format(time.RFC3339))

	runRetentionJob(t, d, cat, now)

	for _, id := range []int64{id1, id6, id7} {
		var cnt int
		d.QueryRow(`SELECT count(*) FROM vacancy WHERE id=?`, id).Scan(&cnt)
		if cnt != 0 {
			t.Errorf("vacancy %d should be removed, still present", id)
		}
	}
	for _, id := range []int64{id2, id3, id4, id5} {
		var cnt int
		d.QueryRow(`SELECT count(*) FROM vacancy WHERE id=?`, id).Scan(&cnt)
		if cnt != 1 {
			t.Errorf("vacancy %d should be kept, not found", id)
		}
	}

	_, out, err := cat.Upsert(ctx, expiryPosting("https://example.com/jobs/e1", twoDaysAgo), "src")
	if err != nil {
		t.Fatalf("re-upsert after removal: %v", err)
	}
	if out != catalog.OutcomeTombstoned {
		t.Errorf("tombstone after removal: got %v, want OutcomeTombstoned", out)
	}
}

func TestRetentionSecondRunRemovesNothing(t *testing.T) {
	d := openTestDB(t)
	now := retentionBase
	cat := catalog.New(d, func() time.Time { return now })
	ctx := context.Background()

	cat.Upsert(ctx, expiryPosting("https://example.com/jobs/once", now.Add(-48*time.Hour)), "src")

	j1 := runRetentionJob(t, d, cat, now)
	if j1.Done != 1 {
		t.Fatalf("first run: done=%d, want 1", j1.Done)
	}
	j2 := runRetentionJob(t, d, cat, now)
	if j2.Done != 0 {
		t.Errorf("second run: done=%d, want 0", j2.Done)
	}
}
