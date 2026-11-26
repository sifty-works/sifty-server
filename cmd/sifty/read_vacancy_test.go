package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/reading"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock      { return &testClock{now: time.Now()} }
func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func insertFullVacancy(t *testing.T, d *db.DB, url, title, company string) (id int64) {
	t.Helper()
	cat := catalog.New(d, func() time.Time { return time.Now() })
	body := "<p>Join our " + title + " team at " + company + ". Full-time position with competitive salary. The role involves building distributed systems and collaborating with cross-functional teams.</p>"
	id, _, err := cat.Upsert(context.Background(), listing.Posting{
		URL:       url,
		Title:     title,
		Company:   company,
		Text:      body,
		TextState: listing.TextFull,
	}, "src")
	if err != nil {
		t.Fatalf("upsert vacancy: %v", err)
	}
	return id
}

func emptyReadingReply() string {
	out := map[string]any{"requirements": []any{}, "conditions": map[string]any{}}
	b, _ := json.Marshal(out)
	return string(b)
}

func runReadVacancyDirect(t *testing.T, d *db.DB, vacancyID, version int64, m models.Model) error {
	t.Helper()
	j := &runner.Job{Subject: fmt.Sprintf("%d/%d", vacancyID, version)}
	return runReadVacancy(context.Background(), j, d, func(_ int64) models.Model { return m })
}

type readVacancyFix struct {
	d  *db.DB
	id int64
	fm *models.FakeModel
	r  *runner.Runner
}

func newReadVacancyFix(t *testing.T, url string) readVacancyFix {
	t.Helper()
	d := openTestDB(t)
	id := insertFullVacancy(t, d, url, "Backend Engineer", "Stackworks")
	fm := &models.FakeModel{}
	r := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r, d, fm)
	return readVacancyFix{d: d, id: id, fm: fm, r: r}
}

func TestReadVacancy_reads_vacancy_and_saves_reading(t *testing.T) {
	d := openTestDB(t)
	first := insertFullVacancy(t, d, "https://jobs.example.com/rv1", "Backend Engineer", "Stackworks")
	second := insertFullVacancy(t, d, "https://jobs.example.com/rv1b", "Backend Engineer", "Northwind")
	insertProfile(t, d, 1, "Backend Engineer")
	fm := &models.FakeModel{}
	fm.AddReply(models.Response{Text: emptyReadingReply()})
	r := runner.New(d, nil, time.Now, slog.Default())
	registerJobKinds(r, d, openStoreWithReading(t), fixedModel{fm}, catalog.New(d, time.Now), slog.New(slog.NewTextHandler(io.Discard, nil)), false, time.Now)
	r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", first)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(ctx)

	waitFor(t, 5*time.Second, func() bool {
		var readerVer string
		d.QueryRow(`SELECT reader_version FROM vacancy_reading WHERE vacancy_id=?`, first).Scan(&readerVer)
		return readerVer != ""
	})
	var readerVer string
	d.QueryRow(`SELECT reader_version FROM vacancy_reading WHERE vacancy_id=?`, first).Scan(&readerVer)
	if readerVer == "import-1" {
		t.Error("reader_version should not be import-1")
	}
	waitFor(t, 5*time.Second, func() bool {
		var n int
		d.QueryRow(`SELECT count(*) FROM job WHERE kind='read_vacancy' AND subject=?`, fmt.Sprintf("%d/1", second)).Scan(&n)
		return n > 0
	})
	var recomputed int
	d.QueryRow(`SELECT count(*) FROM job WHERE kind='recompute_fit'`).Scan(&recomputed)
	if recomputed == 0 {
		t.Error("no recompute_fit job followed the reading")
	}
}

func TestReadVacancy_deleted_vacancy_ends_without_error(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv2")
	fix.d.Exec(`DELETE FROM vacancy WHERE id=?`, fix.id)

	if err := runReadVacancyDirect(t, fix.d, fix.id, 1, fix.fm); err != nil {
		t.Fatalf("expected no error when vacancy deleted, got: %v", err)
	}
	if len(fix.fm.Calls()) != 0 {
		t.Errorf("model should not be called for deleted vacancy")
	}
}

func TestReadVacancy_version_mismatch_ends_without_error(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv3")

	if err := runReadVacancyDirect(t, fix.d, fix.id, 99, fix.fm); err != nil {
		t.Fatalf("expected no error on version mismatch, got: %v", err)
	}
	if len(fix.fm.Calls()) != 0 {
		t.Errorf("model called for a version that moved on")
	}
}

func TestReadVacancy_wait_error_requeues_without_counting_attempt(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv4")
	retryAt := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	fix.fm.AddError(models.WaitError{RetryAfter: retryAt})
	fix.fm.AddReply(models.Response{Text: emptyReadingReply()})

	fix.r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", fix.id)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := fix.r.Last("read_vacancy"); ok && j.State == "queued" && j.Error != "" {
			if j.Attempts != 0 {
				t.Errorf("wait error should not count as attempt: attempts=%d", j.Attempts)
			}
			nb := j.NotBefore.UTC().Truncate(time.Second)
			want := retryAt.UTC().Truncate(time.Second)
			if !nb.Equal(want) {
				t.Errorf("not_before = %v, want %v", nb, want)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not go back to queued after WaitError")
}

func TestReadVacancy_paid_bulk_refused_leaves_job_waiting(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv5")
	fix.fm.AddError(models.ErrBulkOnPaid)
	fix.fm.AddReply(models.Response{Text: emptyReadingReply()})

	fix.r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", fix.id)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := fix.r.Last("read_vacancy"); ok && j.State == "queued" && j.Error != "" {
			if j.Attempts != 0 {
				t.Errorf("paid bulk refused should not count as attempt: attempts=%d", j.Attempts)
			}
			if !j.NotBefore.After(time.Now()) {
				t.Errorf("not_before should be in the future, got %v", j.NotBefore)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not go back to queued after ErrBulkOnPaid")
}

func TestReadVacancy_prompt_too_long_fails_job_at_once(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv-long")
	fix.fm.AddError(fmt.Errorf("%w: length 90000, limit 8000", models.ErrPromptTooLong))

	fix.r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", fix.id)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	var j runner.Job
	waitFor(t, 5*time.Second, func() bool {
		jj, ok := fix.r.Last("read_vacancy")
		if ok && jj.State == "failed" {
			j = jj
			return true
		}
		return false
	})
	if j.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", j.Attempts)
	}
	if n := len(fix.fm.Calls()); n != 1 {
		t.Errorf("model calls = %d, want 1", n)
	}
}

func TestReadVacancy_three_model_errors_fail_job(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv6")
	fix.fm.AddError(errors.New("model error"))
	fix.fm.AddError(errors.New("model error"))
	fix.fm.AddError(errors.New("model error"))

	clk := newTestClock()
	r := runner.New(fix.d, nil, clk.Now, slog.Default())
	registerKindsWith(t, r, fix.d, fix.fm)

	r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", fix.id)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
				clk.Advance(5 * time.Minute)
				r.Wake()
			}
		}
	}()

	var j runner.Job
	waitFor(t, 5*time.Second, func() bool {
		jj, ok := r.Last("read_vacancy")
		if ok && jj.State == "failed" {
			j = jj
			return true
		}
		return false
	})
	if j.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", j.Attempts)
	}
}

type concurrencyModel struct {
	models.Model
	mu      sync.Mutex
	current int
	max     int
}

func (c *concurrencyModel) Complete(ctx context.Context, r models.Request) (models.Response, error) {
	c.mu.Lock()
	c.current++
	if c.current > c.max {
		c.max = c.current
	}
	c.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	resp, err := c.Model.Complete(ctx, r)
	c.mu.Lock()
	c.current--
	c.mu.Unlock()
	return resp, err
}

func TestReadVacancy_two_jobs_run_one_after_the_other(t *testing.T) {
	d := openTestDB(t)
	id1 := insertFullVacancy(t, d, "https://jobs.example.com/rv7a", "Senior Engineer", "Alpha")
	id2 := insertFullVacancy(t, d, "https://jobs.example.com/rv7b", "Lead Engineer", "Beta")

	fm := &models.FakeModel{}
	fm.AddReply(models.Response{Text: emptyReadingReply()})
	fm.AddReply(models.Response{Text: emptyReadingReply()})
	cm := &concurrencyModel{Model: fm}

	r := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r, d, cm)

	r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", id1)})
	r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", id2)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(ctx)

	waitFor(t, 10*time.Second, func() bool {
		var done int
		d.QueryRow(`SELECT count(*) FROM job WHERE kind='read_vacancy' AND state='done'`).Scan(&done)
		return done == 2
	})

	cm.mu.Lock()
	max := cm.max
	cm.mu.Unlock()
	if max != 1 {
		t.Errorf("max concurrent readings = %d, want 1 (serial)", max)
	}
}

func TestReadVacancy_vacancy_deleted_while_reading_saves_nothing(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv10")
	fix.fm.AddReply(models.Response{Text: emptyReadingReply()})

	j := &runner.Job{Subject: fmt.Sprintf("%d/1", fix.id)}
	var capturedVacancyID int64
	err := runReadVacancy(
		context.Background(),
		j,
		fix.d,
		func(vid int64) models.Model {
			capturedVacancyID = vid
			fix.d.Exec(`DELETE FROM vacancy WHERE id=?`, fix.id)
			return fix.fm
		},
	)
	if err != nil {
		t.Fatalf("expected no error when vacancy deleted before save, got: %v", err)
	}
	if capturedVacancyID != fix.id {
		t.Errorf("model fn received vacancyID=%d, want %d", capturedVacancyID, fix.id)
	}
	var readingCount int
	fix.d.QueryRow(`SELECT count(*) FROM vacancy_reading WHERE vacancy_id=?`, fix.id).Scan(&readingCount)
	if readingCount != 0 {
		t.Errorf("expected no reading saved when vacancy deleted, got count=%d", readingCount)
	}
}

func TestReadVacancy_saves_reading_for_correct_version(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv12")
	fix.fm.AddReply(models.Response{Text: emptyReadingReply()})

	if err := runReadVacancyDirect(t, fix.d, fix.id, 1, fix.fm); err != nil {
		t.Fatalf("runReadVacancy: %v", err)
	}

	var savedVersion int64
	fix.d.QueryRow(`SELECT version FROM vacancy_reading WHERE vacancy_id=?`, fix.id).Scan(&savedVersion)
	if savedVersion != 1 {
		t.Errorf("reading version = %d, want 1", savedVersion)
	}
}

func TestReadVacancy_reading_has_correct_reader_version(t *testing.T) {
	fix := newReadVacancyFix(t, "https://jobs.example.com/rv13")
	fix.fm.AddReply(models.Response{Text: emptyReadingReply()})

	if err := runReadVacancyDirect(t, fix.d, fix.id, 1, fix.fm); err != nil {
		t.Fatalf("runReadVacancy: %v", err)
	}

	var savedReaderVer string
	fix.d.QueryRow(`SELECT reader_version FROM vacancy_reading WHERE vacancy_id=?`, fix.id).Scan(&savedReaderVer)
	if savedReaderVer != reading.ReaderVersion() {
		t.Errorf("reader_version = %q, want %q", savedReaderVer, reading.ReaderVersion())
	}
}
