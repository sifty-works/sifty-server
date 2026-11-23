package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func TestRunningJobsAfterRestartResumeExceptDocumentAnalysis(t *testing.T) {
	cases := []struct {
		kind, subject, want string
	}{
		{"recompute_fit", "", "queued"},
		{"read_vacancy", "1/1", "queued"},
		{"shortlist_check", "1/1", "queued"},
		{"retention", "", "queued"},
		{"analyze_document", "1", "interrupted"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			d := openTestDB(t)
			ts := time.Now().UTC().Format(time.RFC3339)
			if _, err := d.Exec(
				`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
				 VALUES (?,?,0,'running',1,?,'{}','',NULL,?,?)`,
				tc.kind, tc.subject, ts, ts, ts,
			); err != nil {
				t.Fatalf("insert running job: %v", err)
			}
			r := runner.New(d, nil, time.Now, slog.Default())
			registerKindsWith(t, r, d, &models.FakeModel{})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r.Start(ctx)

			var state string
			d.QueryRow(`SELECT state FROM job WHERE kind=?`, tc.kind).Scan(&state)
			if state != tc.want {
				t.Errorf("%s after restart: state = %q, want %q", tc.kind, state, tc.want)
			}
		})
	}
}

func TestReadingPaidBulkBlocked(t *testing.T) {
	var reqCount atomic.Int64
	fakeProv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(fakeProv.Close)

	dir := t.TempDir()
	cfg := fmt.Sprintf(`providers:
  paidbox: {type: openai, model: gpt-turbo, cost: paid, endpoint: %q}
work: {reading: paidbox, shortlist_check: paidbox, preparation: paidbox}
`, fakeProv.URL)
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatalf("write sifty.yaml: %v", err)
	}

	seed, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	vid := insertFullVacancy(t, seed, "https://jobs.example.com/paid-read", "Backend Engineer", "Stackworks")
	seed.Exec(`INSERT INTO profile (id,data) VALUES (1,'{"name":"P1"}') ON CONFLICT DO NOTHING`)
	seed.Exec(`INSERT INTO decision (profile,vacancy,stage,reason,decided_at) VALUES (1,?,'shortlist','',?)`,
		vid, time.Now().UTC().Format(time.RFC3339))
	seed.Close()

	_, cancel := startServer(t, dir, false)
	defer cancel()

	probe, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	defer probe.Close()

	waitFor(t, 5*time.Second, func() bool {
		var n int
		probe.QueryRow(`SELECT count(*) FROM job WHERE kind='read_vacancy' AND state='queued' AND error != ''`).Scan(&n)
		return n > 0
	})
	if n := reqCount.Load(); n != 0 {
		t.Errorf("paid provider received %d requests for reading, want 0", n)
	}
	var notBefore string
	probe.QueryRow(`SELECT not_before FROM job WHERE kind='read_vacancy' ORDER BY id DESC LIMIT 1`).Scan(&notBefore)
	at, err := time.Parse(time.RFC3339, notBefore)
	if err != nil {
		t.Fatalf("not_before %q: %v", notBefore, err)
	}
	if wait := time.Until(at); wait < paidBulkDelay-time.Minute {
		t.Errorf("reading waits %v before it tries again, want about %v: the job was put off by an ordinary error, not by the refusal of paid bulk work", wait, paidBulkDelay)
	}
}
