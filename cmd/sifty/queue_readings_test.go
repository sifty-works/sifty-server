package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/runner"
)

const readingProviderYAML = `providers:
  testprovider:
    type: ollama
    model: test
    cost: local
work:
  reading: testprovider
`

func openStoreWithReading(t *testing.T) *config.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sifty.yaml")
	if err := os.WriteFile(path, []byte(readingProviderYAML), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store, result, err := config.Open(path)
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	if result.Err != "" {
		t.Fatalf("config parse error: %s", result.Err)
	}
	return store
}

func openStoreNoReading(t *testing.T) *config.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sifty.yaml")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store, _, err := config.Open(path)
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	return store
}

func postingFull(url, title, company string) listing.Posting {
	return listing.Posting{
		URL:       url,
		Title:     title,
		Company:   company,
		Text:      "We are looking for a " + title + " to join our team.\nFull-time role with competitive pay.",
		TextState: listing.TextFull,
	}
}

func insertProfile(t *testing.T, d *db.DB, id int, roles ...string) {
	t.Helper()
	rolesJSON := `["` + roles[0] + `"`
	for _, r := range roles[1:] {
		rolesJSON += `,"` + r + `"`
	}
	rolesJSON += `]`
	_, err := d.Exec(`INSERT INTO profile (id, data) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`,
		id, `{"name":"Tester","targetRoles":`+rolesJSON+`}`)
	if err != nil {
		t.Fatalf("insert profile: %v", err)
	}
}

func collectQueuedSubjects(t *testing.T, d *db.DB) []string {
	t.Helper()
	rows, err := d.Query(`SELECT subject FROM job WHERE kind='read_vacancy' ORDER BY id`)
	if err != nil {
		t.Fatalf("query queued: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func fakeEnqueue(d *db.DB) func(runner.Spec) (int64, error) {
	return func(s runner.Spec) (int64, error) {
		res, err := d.Exec(
			`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
			 VALUES (?,?,?,'queued',0,datetime('now'),'{}','',NULL,datetime('now'),datetime('now'))`,
			s.Kind, s.Subject, s.Priority)
		if err != nil {
			return 0, err
		}
		id, _ := res.LastInsertId()
		return id, nil
	}
}

func fixedNow() func() time.Time {
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

const knownReaderVersion = "reading/1+aabbccdd"
const oldReaderVersion = "reading/1+00000000"

type queueFix struct {
	d   *db.DB
	cat *catalog.Catalog
	ctx context.Context
}

func newQueueFix(t *testing.T) queueFix {
	t.Helper()
	d := openTestDB(t)
	return queueFix{d: d, cat: catalog.New(d, fixedNow()), ctx: context.Background()}
}

func TestQueueReadings_full_text_visible_vacancy_is_queued(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	id, _, err := fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe1", "Platform Engineer", "Stackworks"), "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings: %v", err)
	}

	subjects := collectQueuedSubjects(t, fix.d)
	if len(subjects) != 1 {
		t.Fatalf("queued count = %d, want 1; subjects=%v", len(subjects), subjects)
	}
	want := subjects[0]
	if want != fmt.Sprintf("%d/1", id) {
		t.Errorf("subject = %q, want %d/1", want, id)
	}

	var priority int
	fix.d.QueryRow(`SELECT priority FROM job WHERE kind='read_vacancy'`).Scan(&priority)
	if priority != 0 {
		t.Errorf("priority = %d, want 0 (new reading)", priority)
	}
}

func TestQueueReadings_summary_only_vacancy_is_not_queued(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	fix.cat.Upsert(fix.ctx, listing.Posting{
		URL:       "https://jobs.example.com/sum1",
		Title:     "Platform Engineer",
		Company:   "Stackworks",
		Text:      "Short summary.",
		TextState: listing.TextSummary,
	}, "src")

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings: %v", err)
	}

	if subs := collectQueuedSubjects(t, fix.d); len(subs) != 0 {
		t.Errorf("expected no queued jobs, got %v", subs)
	}
}

func TestQueueReadings_hidden_vacancy_is_not_queued(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Data Analyst")
	fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe2", "Platform Engineer", "Stackworks"), "src")

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings: %v", err)
	}

	if subs := collectQueuedSubjects(t, fix.d); len(subs) != 0 {
		t.Errorf("expected no queued jobs for hidden vacancy, got %v", subs)
	}
}

func TestQueueReadings_vacancy_with_decision_is_visible(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Data Analyst")
	id, _, err := fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe3", "Platform Engineer", "Stackworks"), "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	fix.d.Exec(`INSERT INTO decision (profile,vacancy,stage,reason,decided_at) VALUES (1,?,'shortlist','','2026-01-01T00:00:00Z')`, id)

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings: %v", err)
	}

	if subs := collectQueuedSubjects(t, fix.d); len(subs) != 1 {
		t.Errorf("expected 1 queued job for vacancy with decision, got %v", subs)
	}
}

func TestQueueReadings_imported_reading_is_not_reread(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	id, _, err := fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe4", "Platform Engineer", "Stackworks"), "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	fix.d.Exec(`INSERT INTO vacancy_reading (vacancy_id,version,reader_version,record,created) VALUES (?,1,'import-1','{}','2026-01-01T00:00:00Z')`, id)

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings: %v", err)
	}

	if subs := collectQueuedSubjects(t, fix.d); len(subs) != 0 {
		t.Errorf("imported reading should not be reread, got %v", subs)
	}
}

func TestQueueReadings_older_reader_version_queued_at_low_priority(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	id, _, err := fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe5", "Platform Engineer", "Stackworks"), "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	fix.d.Exec(`INSERT INTO vacancy_reading (vacancy_id,version,reader_version,record,created) VALUES (?,1,?,'{}','2026-01-01T00:00:00Z')`, id, oldReaderVersion)

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings: %v", err)
	}

	subs := collectQueuedSubjects(t, fix.d)
	if len(subs) != 1 {
		t.Fatalf("expected 1 queued job, got %v", subs)
	}
	var priority int
	fix.d.QueryRow(`SELECT priority FROM job WHERE kind='read_vacancy'`).Scan(&priority)
	if priority != -1 {
		t.Errorf("priority = %d, want -1 (re-reading at low priority)", priority)
	}
}

func TestQueueReadings_queued_text_is_not_queued_again(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	id, _, err := fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe6", "Platform Engineer", "Stackworks"), "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	subject := fmt.Sprintf("%d/1", id)
	fix.d.Exec(`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
	        VALUES ('read_vacancy',?,'0','queued',0,datetime('now'),'{}','',NULL,datetime('now'),datetime('now'))`, subject)

	prevCount := func() int {
		var n int
		fix.d.QueryRow(`SELECT count(*) FROM job WHERE kind='read_vacancy'`).Scan(&n)
		return n
	}
	before := prevCount()

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings: %v", err)
	}

	if prevCount() != before {
		t.Errorf("queueing twice should not add more jobs; already had %d", before)
	}
}

func TestQueueReadings_failed_text_is_not_queued_again(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	id, _, err := fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe7", "Platform Engineer", "Stackworks"), "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	subject := fmt.Sprintf("%d/1", id)
	fix.d.Exec(`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
	        VALUES ('read_vacancy',?,'0','failed',3,datetime('now'),'{}','model error',NULL,datetime('now'),datetime('now'))`, subject)

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings: %v", err)
	}

	var total int
	fix.d.QueryRow(`SELECT count(*) FROM job WHERE kind='read_vacancy'`).Scan(&total)
	if total != 1 {
		t.Errorf("failed job should not be requeued; job count = %d, want 1", total)
	}
}

func TestQueueReadings_offline_queues_nothing(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe8", "Platform Engineer", "Stackworks"), "src")

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, true, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings offline: %v", err)
	}

	if subs := collectQueuedSubjects(t, fix.d); len(subs) != 0 {
		t.Errorf("offline: expected no queued jobs, got %v", subs)
	}
}

func TestQueueReadings_no_provider_queues_nothing(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe9", "Platform Engineer", "Stackworks"), "src")

	store := openStoreNoReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings no provider: %v", err)
	}

	if subs := collectQueuedSubjects(t, fix.d); len(subs) != 0 {
		t.Errorf("no provider: expected no queued jobs, got %v", subs)
	}
}

func TestQueueReadings_active_read_vacancy_suppresses_all_queueing(t *testing.T) {
	fix := newQueueFix(t)
	insertProfile(t, fix.d, 1, "Platform Engineer")
	fix.cat.Upsert(fix.ctx, postingFull("https://jobs.example.com/pe10", "Platform Engineer", "Stackworks"), "src")

	fix.d.Exec(`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
	        VALUES ('read_vacancy','999/1',0,'running',1,datetime('now'),'{}','',NULL,datetime('now'),datetime('now'))`)

	store := openStoreWithReading(t)
	if err := queueReadings(fix.ctx, fix.d, fakeEnqueue(fix.d), store, false, knownReaderVersion); err != nil {
		t.Fatalf("queueReadings with active job: %v", err)
	}

	var total int
	fix.d.QueryRow(`SELECT count(*) FROM job WHERE kind='read_vacancy'`).Scan(&total)
	if total != 1 {
		t.Errorf("active job: expected only the pre-existing 1 job, got %d", total)
	}
}
