package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
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
	return runRetentionJobWithLimit(t, d, cat, now, 0)
}

func runRetentionJobWithLimit(t *testing.T, d *db.DB, cat *catalog.Catalog, now time.Time, months int) runner.Job {
	t.Helper()
	r := runner.New(d, nil, func() time.Time { return now }, slog.Default())
	path := filepath.Join(t.TempDir(), "sifty.yaml")
	if months > 0 {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("collection:\n  max_age_months: %d\n", months)), 0600); err != nil {
			t.Fatalf("write settings: %v", err)
		}
	}
	store, _, err := config.Open(path)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	registerJobKinds(r, d, store, fixedModel{&models.FakeModel{}}, cat, fetchtest.NewMap(), slog.New(slog.NewTextHandler(io.Discard, nil)), false, func() time.Time { return now })
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

func TestRetentionRemovesUndecidedVacanciesOlderThanCollectionAgeLimit(t *testing.T) {
	d := openTestDB(t)
	now := retentionBase
	day := 24 * time.Hour
	cat := catalog.New(d, func() time.Time { return now })
	ctx := context.Background()
	d.Exec(`INSERT INTO profile (id,data) VALUES (2,'{"name":"P2"}')`)
	decide := func(profile int, stage string) func(int64) {
		return func(id int64) {
			d.Exec(`INSERT INTO decision (profile,vacancy,stage,reason,decided_at) VALUES (?,?,?,'',?)`,
				profile, id, stage, now.Format(time.RFC3339))
		}
	}
	apply := func(id int64) {
		d.Exec(`INSERT INTO application (vacancy,applied_at,profile,profile_name) VALUES (?,?,1,'P1')`, id, now.Format(time.RFC3339))
	}
	cases := []struct {
		name                 string
		published, seen, exp time.Time
		mark                 func(int64)
		removed              bool
	}{
		{"published 40 days ago", now.Add(-40 * day), time.Time{}, time.Time{}, nil, true},
		{"published 25 days ago", now.Add(-25 * day), time.Time{}, time.Time{}, nil, false},
		{"undated first seen 40 days ago", time.Time{}, now.Add(-40 * day), time.Time{}, nil, true},
		{"undated first seen 10 days ago", time.Time{}, now.Add(-10 * day), time.Time{}, nil, false},
		{"published 25 days ago first seen 60 days ago", now.Add(-25 * day), now.Add(-60 * day), time.Time{}, nil, false},
		{"shortlisted", now.Add(-40 * day), time.Time{}, time.Time{}, decide(1, "shortlist"), false},
		{"dismissed", now.Add(-40 * day), time.Time{}, time.Time{}, decide(1, "dismissed"), false},
		{"decided in the second profile only", now.Add(-40 * day), time.Time{}, time.Time{}, decide(2, "shortlist"), false},
		{"applied", now.Add(-40 * day), time.Time{}, time.Time{}, func(id int64) { decide(1, "applied")(id); apply(id) }, false},
		{"application without decision", now.Add(-40 * day), time.Time{}, time.Time{}, apply, false},
		{"expired recent", now.Add(-5 * day), time.Time{}, now.Add(-2 * day), nil, true},
	}
	ids := make([]int64, len(cases))
	postings := make([]listing.Posting, len(cases))
	for i, c := range cases {
		p := expiryPosting(fmt.Sprintf("https://example.com/jobs/age%d", i), c.exp)
		p.Published = c.published
		postings[i] = p
		ids[i], _, _ = cat.Upsert(ctx, p, "src")
		if !c.seen.IsZero() {
			d.Exec(`UPDATE vacancy SET first_seen=? WHERE id=?`, c.seen.Format(time.RFC3339), ids[i])
		}
		if c.mark != nil {
			c.mark(ids[i])
		}
	}

	t.Run("no limit set keeps the old undecided vacancy", func(t *testing.T) {
		if j := runRetentionJobWithLimit(t, d, cat, now, 0); j.Done != 1 {
			t.Errorf("removed count: got %d, want 1", j.Done)
		}
		var cnt int
		d.QueryRow(`SELECT count(*) FROM vacancy WHERE id=?`, ids[0]).Scan(&cnt)
		if cnt != 1 {
			t.Error("old undecided vacancy removed without a limit")
		}
	})

	if j := runRetentionJobWithLimit(t, d, cat, now, 1); j.Done != 2 {
		t.Errorf("removed count: got %d, want 2", j.Done)
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var cnt int
			d.QueryRow(`SELECT count(*) FROM vacancy WHERE id=?`, ids[i]).Scan(&cnt)
			if (cnt == 0) != c.removed {
				t.Errorf("removed=%v, want %v", cnt == 0, c.removed)
			}
			if c.removed {
				if _, out, _ := cat.Upsert(ctx, postings[i], "src"); out != catalog.OutcomeTombstoned {
					t.Errorf("collection after removal: got %v, want OutcomeTombstoned", out)
				}
			}
		})
	}
}
