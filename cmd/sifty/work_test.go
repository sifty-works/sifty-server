package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func getWork(t *testing.T, base string) workResp {
	t.Helper()
	body, status := apiGet(t, base, "/api/work")
	if status != http.StatusOK {
		t.Fatalf("GET /api/work: status %d", status)
	}
	b, _ := json.Marshal(body)
	var out workResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode work: %v", err)
	}
	return out
}

func waitingFor(w workResp, kind string) *workWaiting {
	for i := range w.Waiting {
		if w.Waiting[i].Kind == kind {
			return &w.Waiting[i]
		}
	}
	return nil
}

type workServe struct {
	base      string
	providerN func() int
}

func startWorkServe(t *testing.T, cost, extraYAML string, handler http.HandlerFunc, seed func(d *db.DB)) workServe {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	prov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(prov.Close)

	dir := t.TempDir()
	cfg := fmt.Sprintf("providers:\n  box: {type: openai, model: m, cost: %s, endpoint: %q}\nwork: {reading: box, shortlist_check: box, preparation: box}\n%s", cost, prov.URL, extraYAML)
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	d, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	a := insertFullVacancy(t, d, "https://jobs.example.com/work-a", "Backend Engineer", "Stackworks")
	b := insertFullVacancy(t, d, "https://jobs.example.com/work-b", "Data Engineer", "Northwind")
	ts := time.Now().UTC().Format(time.RFC3339)
	for _, id := range []int64{a, b} {
		if _, err := d.Exec(`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
			VALUES ('read_vacancy',?,0,'queued',0,?,'{}','',NULL,?,?)`, fmt.Sprintf("%d/1", id), ts, ts, ts); err != nil {
			t.Fatalf("seed job: %v", err)
		}
	}
	if seed != nil {
		seed(d)
	}
	d.Close()

	port, cancel := startServer(t, dir, false)
	t.Cleanup(cancel)
	return workServe{base: fmt.Sprintf("http://127.0.0.1:%d", port), providerN: func() int { mu.Lock(); defer mu.Unlock(); return calls }}
}

func TestWorkExplainsAHeldProviderWithoutItsErrorText(t *testing.T) {
	const secret = "upstream-secret-detail"
	s := startWorkServe(t, "local", "", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, secret, http.StatusInternalServerError)
	}, nil)
	var q *workWaiting
	waitFor(t, 5*time.Second, func() bool {
		q = waitingFor(getWork(t, s.base), "read_vacancy")
		return q != nil && q.Count == 2 && q.Reason != nil && q.Reason.Code == "held"
	})
	if q.Reason.Text == "" || strings.Contains(q.Reason.Text, secret) || strings.Contains(q.Reason.Text, "status 500") {
		t.Errorf("reason text = %q, want a sentence without the provider's error text", q.Reason.Text)
	}
}

func TestLogsListRecentFailuresWithSentencesAndNoGoText(t *testing.T) {
	d := openTestDB(t)
	tooLong := insertFullVacancy(t, d, "https://jobs.example.com/f1", "Long Posting", "Acme")
	broken := insertFullVacancy(t, d, "https://jobs.example.com/f2", "Odd Posting", "Globex")
	old := insertFullVacancy(t, d, "https://jobs.example.com/f3", "Old Posting", "Initech")

	clk := newTestClock()
	r := runner.New(d, nil, clk.Now, slog.Default())
	fm := &models.FakeModel{}
	fm.AddError(fmt.Errorf("%w: length 90000, limit 8000", models.ErrPromptTooLong))
	for range 3 {
		fm.AddError(errors.New("dial tcp 10.9.8.7: connection refused"))
	}
	registerKindsWith(t, r, d, fm)

	oldAt := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := d.Exec(`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
		VALUES ('read_vacancy',?,0,'failed',3,?,'{}','Failed; the log has the details under job #1',NULL,?,?)`,
		fmt.Sprintf("%d/1", old), oldAt, oldAt, oldAt); err != nil {
		t.Fatalf("seed old failure: %v", err)
	}

	base := newRouteServer(t, d, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(ctx)
	r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", tooLong)})
	waitFor(t, 5*time.Second, func() bool { return len(getLogs(t, base).Failed) == 1 })
	r.Enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/1", broken)})
	go func() {
		for ctx.Err() == nil {
			time.Sleep(5 * time.Millisecond)
			clk.Advance(5 * time.Minute)
			r.Wake()
		}
	}()
	waitFor(t, 5*time.Second, func() bool { return len(getLogs(t, base).Failed) == 2 })

	failed := getLogs(t, base).Failed
	newest, first := failed[0], failed[1]
	if first.Reason != "The vacancy text is too long for the chosen model. Choose a model with a longer context in Settings." || first.Subject != "Long Posting at Acme" {
		t.Errorf("prompt-too-long failure = %+v", first)
	}
	wantGeneric := fmt.Sprintf("Failed; the log has the details under job #%d", newest.ID)
	if newest.Reason != wantGeneric || newest.Subject != "Odd Posting at Globex" || newest.Label != "Reading vacancy text" {
		t.Errorf("unknown-error failure = %+v, want reason %q", newest, wantGeneric)
	}
	if strings.Contains(newest.Reason, "10.9.8.7") {
		t.Errorf("Go error text reached the answer: %q", newest.Reason)
	}
	for _, f := range failed {
		if f.Subject == "Old Posting at Initech" {
			t.Errorf("a failure older than 7 days is listed: %+v", f)
		}
	}
}

func TestWorkAnswersWithAnErrorWhenJobsCannotBeRead(t *testing.T) {
	d := openTestDB(t)
	r := runner.New(d, nil, time.Now, slog.Default())
	base := newRouteServer(t, d, r)
	d.Close()
	if _, status := apiGet(t, base, "/api/work"); status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (an unreadable queue is not an empty one)", status)
	}
}
