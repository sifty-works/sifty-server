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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/reading"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type checkFix struct {
	d       *db.DB
	r       *runner.Runner
	fm      *models.FakeModel
	base    string
	vacancy int64
	clock   *testClock
}

type checkOpts struct {
	idle bool
}

func seedReading(t *testing.T, d *db.DB, vacancy int64, created string, texts ...string) {
	t.Helper()
	rec := reading.Record{}
	for _, text := range texts {
		rec.Requirements = append(rec.Requirements, reading.Requirement{Text: text, Kind: "language", Level: "required"})
	}
	rec.Requirements = append(rec.Requirements, reading.Requirement{Text: "Go", Kind: "skill", Level: "required"})
	b, _ := json.Marshal(rec)
	if _, err := d.Exec(`INSERT OR REPLACE INTO vacancy_reading (vacancy_id, version, reader_version, record, created) VALUES (?, 1, ?, ?, ?)`,
		vacancy, reading.ReaderVersion(), string(b), created); err != nil {
		t.Fatalf("seed reading: %v", err)
	}
}

func newCheckFix(t *testing.T, o checkOpts) checkFix {
	t.Helper()
	d := openTestDB(t)
	clk := newTestClock()
	r := runner.New(d, nil, clk.Now, slog.Default())
	fm := &models.FakeModel{}
	registerKindsAt(t, r, d, fm, catalog.New(d, time.Now), clk.Now)
	base := newRouteServerWithReaders(t, d, r, candidate.Readers{PDF: candidate.ProgramText{Program: filepath.Join(t.TempDir(), "absent")}})
	if !o.idle {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go r.Start(ctx)
	}
	vacancy := insertFullVacancy(t, d, "https://jobs.example.com/check-1", "Backend Engineer", "Stackworks")
	seedReading(t, d, vacancy, "2026-01-01T00:00:00Z", "Fluent German")
	f := checkFix{d: d, r: r, fm: fm, base: base, vacancy: vacancy, clock: clk}
	f.send(t, http.MethodPatch, "/api/me", map[string]any{
		"skills":       []map[string]any{{"name": "Go", "level": "Proficient"}},
		"languages":    []map[string]any{{"name": "German", "level": "C1"}},
		"email":        "someone@example.test",
		"legalName":    "Legal Q. Example",
		"displayName":  "Display Example",
		"phone":        "+000 000 0000",
		"linkedin":     "https://linkedin.example.test/in/someone",
		"github":       "https://github.example.test/someone",
		"x":            "https://x.example.test/someone",
		"website":      "https://someone.example.test",
		"demographics": map[string]any{"gender": "demographic-marker"},
	})
	f.send(t, http.MethodPatch, "/api/profiles/1", map[string]any{"name": "Profile Example", "targetRoles": []string{"Backend Engineer"}})
	return f
}

func (f checkFix) send(t *testing.T, method, path string, payload any) map[string]any {
	t.Helper()
	out, status := apiSend(t, method, f.base, path, payload)
	if status != http.StatusOK {
		t.Fatalf("%s %s: status %d: %v", method, path, status, out)
	}
	return out
}

func (f checkFix) move(t *testing.T, from, to string) {
	t.Helper()
	f.send(t, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/decision", f.vacancy), map[string]any{"profile": 1, "from": from, "to": to})
}

func (f checkFix) view(t *testing.T) map[string]any {
	t.Helper()
	out, status := apiGet(t, f.base, fmt.Sprintf("/api/vacancies/%d?profile=1", f.vacancy))
	if status != http.StatusOK {
		t.Fatalf("GET vacancy: status %d: %v", status, out)
	}
	return out
}

func (f checkFix) check(t *testing.T) map[string]any {
	t.Helper()
	c, _ := f.view(t)["check"].(map[string]any)
	return c
}

func (f checkFix) idle(t *testing.T) {
	t.Helper()
	waitFor(t, 5*time.Second, func() bool {
		var n int
		f.d.QueryRow(`SELECT count(*) FROM job WHERE kind='shortlist_check' AND state IN ('queued','running')`).Scan(&n)
		return n == 0
	})
}

func (f checkFix) jobs(t *testing.T) int {
	t.Helper()
	var n int
	f.d.QueryRow(`SELECT count(*) FROM job WHERE kind='shortlist_check'`).Scan(&n)
	return n
}

func (f checkFix) answers(t *testing.T) []any {
	t.Helper()
	a, _ := f.check(t)["answers"].([]any)
	return a
}

func germanReply() models.Response {
	return checkReply(map[string]string{"requirement": "Fluent German", "verdict": "met", "reason": "The candidate lists German at C1.", "excerpt": "German (C1)"})
}

func checkReply(answers ...map[string]string) models.Response {
	b, _ := json.Marshal(map[string]any{"answers": answers})
	return models.Response{Text: string(b)}
}

func TestShortlistingQueuesOneCheckWhoseAnswerStandsBesideTheOpenRequirement(t *testing.T) {
	f := newCheckFix(t, checkOpts{})
	f.fm.AddReply(germanReply())
	waitFor(t, 5*time.Second, func() bool {
		var n int
		f.d.QueryRow(`SELECT count(*) FROM job WHERE kind='recompute_fit' AND state IN ('queued','running')`).Scan(&n)
		return n == 0
	})
	meBefore, _ := apiGet(t, f.base, "/api/me")
	bandBefore := f.view(t)["band"]
	fitBefore := f.view(t)["fit"]

	f.move(t, "inbox", "shortlist")
	waitFor(t, 5*time.Second, func() bool { return len(f.answers(t)) == 1 })
	f.idle(t)

	if n := len(f.fm.Calls()); n != 1 {
		t.Fatalf("model calls = %d, want 1", n)
	}
	a := f.answers(t)[0].(map[string]any)
	if a["requirement"] != "Fluent German" || a["verdict"] != "✓" || a["excerpt"] != "German (C1)" || a["reason"] == "" {
		t.Errorf("answer = %v", a)
	}
	view := f.view(t)
	if !reflect.DeepEqual(view["band"], bandBefore) || !reflect.DeepEqual(view["fit"], fitBefore) {
		t.Errorf("Fits you or the requirements counter changed:\nband %v -> %v\nfit %v -> %v", bandBefore, view["band"], fitBefore, view["fit"])
	}
	if meAfter, _ := apiGet(t, f.base, "/api/me"); !reflect.DeepEqual(meBefore, meAfter) {
		t.Errorf("My data changed: %v -> %v", meBefore, meAfter)
	}
	if c := f.check(t); c["outdated"] != false || c["retry"] != false || c["state"] != nil {
		t.Errorf("a finished check carries state or outdated mark: %v", c)
	}
}

func TestTextHandedToTheModelHoldsSkillsAndEvidenceButNoContactsLegalNameOrFormAnswers(t *testing.T) {
	f := newCheckFix(t, checkOpts{})
	f.fm.AddReply(germanReply())
	f.send(t, http.MethodPost, "/api/qa", map[string]any{"question": "Why do you want this job?", "answer": "Because of the unique-form-answer-marker."})
	doc := f.send(t, http.MethodPost, "/api/documents", map[string]any{"name": "cv.txt", "text": "Payment systems engineer with unique-evidence-marker."})
	f.send(t, http.MethodPatch, fmt.Sprintf("/api/documents/%.0f", doc["id"]), map[string]any{"evidence": true})

	f.move(t, "inbox", "shortlist")
	f.idle(t)

	calls := f.fm.Calls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(calls))
	}
	prompt := calls[0].Prompt
	for _, want := range []string{"Go (Proficient)", "German (C1)", "unique-evidence-marker", "Fluent German"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, banned := range []string{
		"someone@example.test", "Legal Q. Example", "unique-form-answer-marker", "Display Example",
		"+000 000 0000", "linkedin.example.test", "github.example.test", "x.example.test",
		"someone.example.test", "demographic-marker", "Profile Example",
	} {
		if strings.Contains(prompt, banned) {
			t.Errorf("prompt holds %q", banned)
		}
	}
}

func TestMovingOutOfShortlistAndBackWithNothingChangedMakesNoSecondCall(t *testing.T) {
	f := newCheckFix(t, checkOpts{})
	f.fm.AddReply(germanReply())
	f.move(t, "inbox", "shortlist")
	f.idle(t)
	f.move(t, "shortlist", "inbox")
	f.move(t, "inbox", "shortlist")
	f.view(t)
	f.idle(t)

	if n := len(f.fm.Calls()); n != 1 {
		t.Errorf("model calls = %d, want 1", n)
	}
	if n := f.jobs(t); n != 1 {
		t.Errorf("checks queued = %d, want 1", n)
	}
	f.move(t, "shortlist", "inbox")
	if c := f.view(t)["check"]; c != nil {
		t.Errorf("a vacancy in Inbox shows a check: %v", c)
	}
}

func TestSavingMyDataMakesNoCallMarksTheCheckOutdatedAndOpeningRunsItOnce(t *testing.T) {
	f := newCheckFix(t, checkOpts{})
	f.fm.AddReply(germanReply())
	f.fm.AddReply(checkReply(map[string]string{"requirement": "Fluent German", "verdict": "met", "reason": "The candidate lists German at C2.", "excerpt": "German (C2)"}))
	f.move(t, "inbox", "shortlist")
	f.idle(t)

	f.send(t, http.MethodPatch, "/api/me", map[string]any{"languages": []map[string]any{{"name": "German", "level": "C2"}}})
	time.Sleep(50 * time.Millisecond)
	if n := len(f.fm.Calls()); n != 1 {
		t.Fatalf("saving My data made a model call: calls = %d", n)
	}
	if n := f.jobs(t); n != 1 {
		t.Fatalf("saving My data queued a check: jobs = %d", n)
	}

	opened := f.check(t)
	if opened["outdated"] != true || !strings.HasPrefix(fmt.Sprint(opened["note"]), "Based on your data of ") || opened["made"] == "" {
		t.Errorf("outdated check = %v", opened)
	}
	if len(opened["answers"].([]any)) != 1 {
		t.Errorf("the old answer is not shown while the check is outdated: %v", opened)
	}
	f.idle(t)
	if n := len(f.fm.Calls()); n != 2 {
		t.Errorf("opening the page ran the check %d times in total, want 2", n)
	}
	if c := f.check(t); c["outdated"] != false {
		t.Errorf("check still outdated after it ran again: %v", c)
	}
	if n := len(f.fm.Calls()); n != 2 {
		t.Errorf("opening a current check made a call: %d", n)
	}
}

func TestOpeningTwiceWhileTheCheckIsWaitingQueuesNothingMore(t *testing.T) {
	f := newCheckFix(t, checkOpts{idle: true})
	f.move(t, "inbox", "shortlist")
	f.view(t)
	f.view(t)
	if n := f.jobs(t); n != 1 {
		t.Errorf("jobs = %d, want 1", n)
	}
}

func TestReadingTheSameTextAgainWithTheSameReaderMarksTheCheckOutdated(t *testing.T) {
	f := newCheckFix(t, checkOpts{idle: true})
	f.fm.AddReply(germanReply())
	f.move(t, "inbox", "shortlist")
	runCheckJobs(t, f)
	if c := f.check(t); c["outdated"] != false {
		t.Fatalf("check outdated before the text was read again: %v", c)
	}
	if err := catalog.SaveReading(context.Background(), f.d, f.vacancy, 1, reading.ReaderVersion(), reading.Record{Requirements: []reading.Requirement{
		{Text: "Fluent German", Kind: "language", Level: "required"}, {Text: "Go", Kind: "skill", Level: "required"}}}); err != nil {
		t.Fatalf("save reading: %v", err)
	}
	if c := f.check(t); c["outdated"] != true {
		t.Errorf("check not outdated after the same text was read again: %v", c)
	}
}

func runCheckJobs(t *testing.T, f checkFix) {
	t.Helper()
	var ids []int64
	rows, _ := f.d.Query(`SELECT id FROM job WHERE kind='shortlist_check' AND state='queued' ORDER BY id`)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		var subject string
		f.d.QueryRow(`SELECT subject FROM job WHERE id=?`, id).Scan(&subject)
		if err := runShortlistCheck(context.Background(), &runner.Job{Subject: subject}, f.d, func(int64) models.Model { return f.fm }, time.Now); err != nil {
			t.Fatalf("run check: %v", err)
		}
		f.d.Exec(`UPDATE job SET state='done' WHERE id=?`, id)
	}
}

func TestStoredAnswerWhoseRequirementTextMatchesNoCurrentLineIsNotShown(t *testing.T) {
	f := newCheckFix(t, checkOpts{idle: true})
	f.fm.AddReply(germanReply())
	f.move(t, "inbox", "shortlist")
	runCheckJobs(t, f)
	if len(f.answers(t)) != 1 {
		t.Fatalf("answer not shown before the requirements changed")
	}
	seedReading(t, f.d, f.vacancy, "2026-02-02T00:00:00Z", "German fluency")
	if a := f.answers(t); len(a) != 0 {
		t.Errorf("answer for a requirement text that no longer exists is shown: %v", a)
	}
}

func TestOpeningThePageAfterAFailedCheckQueuesNothingAndRetryMakesExactlyOneMoreCall(t *testing.T) {
	f := newCheckFix(t, checkOpts{idle: true})
	f.move(t, "inbox", "shortlist")
	for range 3 {
		f.fm.AddError(errors.New("model unreachable"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.r.Start(ctx)
	go func() {
		for ctx.Err() == nil {
			time.Sleep(5 * time.Millisecond)
			f.clock.Advance(5 * time.Minute)
			f.r.Wake()
		}
	}()
	waitFor(t, 5*time.Second, func() bool {
		var n int
		f.d.QueryRow(`SELECT count(*) FROM job WHERE kind='shortlist_check' AND state='failed'`).Scan(&n)
		return n == 1
	})

	calls := len(f.fm.Calls())
	f.check(t)
	f.check(t)
	if n := f.jobs(t); n != 1 {
		t.Errorf("opening the page after a failure queued another check: jobs = %d", n)
	}
	if n := len(f.fm.Calls()); n != calls {
		t.Errorf("opening the page after a failure called the model %d more times", n-calls)
	}

	f.fm.AddReply(germanReply())
	f.send(t, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/shortlist-check/retry", f.vacancy), map[string]any{"profile": 1})
	waitFor(t, 5*time.Second, func() bool { return len(f.answers(t)) == 1 })
	if n := len(f.fm.Calls()); n != calls+1 {
		t.Errorf("the retry made %d calls, want 1", n-calls)
	}
}

func TestPaidProviderWithoutPaidBulkAllowanceEndsTheCheckWithItsReason(t *testing.T) {
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
	vid := insertFullVacancy(t, seed, "https://jobs.example.com/paid-check", "Backend Engineer", "Stackworks")
	seedReading(t, seed, vid, "2026-01-01T00:00:00Z", "Fluent German")
	seed.Close()

	port, cancel := startServer(t, dir, false)
	defer cancel()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if _, status := apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/vacancies/%d/decision", vid), map[string]any{"profile": 1, "from": "inbox", "to": "shortlist"}); status != http.StatusOK {
		t.Fatalf("decision: status %d", status)
	}

	var check map[string]any
	waitFor(t, 5*time.Second, func() bool {
		view, _ := apiGet(t, base, fmt.Sprintf("/api/vacancies/%d?profile=1", vid))
		check, _ = view["check"].(map[string]any)
		return check != nil && check["retry"] == true
	})
	if state := fmt.Sprint(check["state"]); !strings.Contains(state, "Bulk model work is not allowed on a paid provider") {
		t.Errorf("state = %q", state)
	}
	if n := reqCount.Load(); n != 0 {
		t.Errorf("paid provider received %d requests, want 0", n)
	}
}
