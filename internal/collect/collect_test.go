package collect_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/collect"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/server"
)

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

type env struct {
	srv   *httptest.Server
	fm    *fetchtest.Map
	store *config.Store
	r     *runner.Runner
	col   *collect.Collector
	db    *db.DB
	dir   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvClock(t, func() time.Time { return base }, false)
}

func addSource(t *testing.T, e *env, url string) {
	t.Helper()
	e.store.Update(func(c *config.Config) error {
		c.Sources = []config.SourceConfig{{URL: url, Enabled: true}}
		return nil
	})
}

func setSources(t *testing.T, e *env, urls ...string) {
	t.Helper()
	srcs := make([]config.SourceConfig, len(urls))
	for i, u := range urls {
		srcs[i] = config.SourceConfig{URL: u, Enabled: true}
	}
	e.store.Update(func(c *config.Config) error { c.Sources = srcs; return nil })
}

func runCollect(t *testing.T, e *env) runner.Job {
	t.Helper()
	e.r.Enqueue(runner.Spec{Kind: "collect"})
	return waitJob(t, e.r)
}

func newEnvClock(t *testing.T, now func() time.Time, offline bool) *env {
	t.Helper()
	database := openDB(t)
	dir := t.TempDir()
	store, _, err := config.Open(filepath.Join(dir, "sifty.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fm := fetchtest.NewMap()
	r := runner.New(database, map[string]int{"net": 4, "cpu": 1, "browser": 1}, now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cat := catalog.New(database, now)
	col := collect.New(store, cat, r, fm, database, offline, now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	col.Register()

	h := server.New(server.Options{
		Version: "test", Port: 19999,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Status: col.StatusFunc(),
	}, func(mux *http.ServeMux) {
		col.RegisterRoutes(mux)
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Start(ctx)

	return &env{srv: srv, fm: fm, store: store, r: r, col: col, db: database, dir: dir}
}

func (e *env) get(t *testing.T, path string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
	req.Host = "127.0.0.1:19999"
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func (e *env) mutate(t *testing.T, method, path string, body map[string]any) (map[string]any, int) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(b))
	req.Host = "127.0.0.1:19999"
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}

func waitJob(t *testing.T, r *runner.Runner) runner.Job {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if j, ok := r.Last("collect"); ok && (j.State == "done" || j.State == "failed") {
			return j
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for collect job to finish")
			return runner.Job{}
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

const ghBoardResponse = `{"company":{"name":"Acme Corp"},
"jobs":[{"id":1,"title":"Engineer","absolute_url":"https://boards.greenhouse.io/acme/jobs/1","content":"<p>Job text</p>"},
{"id":2,"title":"Designer","absolute_url":"https://boards.greenhouse.io/acme/jobs/2","content":"<p>Design text</p>"}]}`

const feedXML = `<?xml version="1.0"?>
<rss version="2.0"><channel>
<item><title>Go Developer</title><company>Northwind</company><link>https://feeds.example.com/jobs/3</link><description><![CDATA[<p>Feed job</p>]]></description></item>
</channel></rss>`

const emptyFeedXML = `<?xml version="1.0"?>
<rss version="2.0"><channel></channel></rss>`

const badPageHTML = `<html><body><p>Not a job site</p></body></html>`

func TestRunWithMixedSourcesEndsDone(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/json", Body: []byte(ghBoardResponse)}})
	e.fm.Set("https://feeds.example.com/jobs.xml",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(feedXML)}})
	e.fm.Set("https://failing.example.com/jobs",
		fetchtest.MapEntry{Err: fetch.ErrNotFound})

	setSources(t, e, "https://boards.greenhouse.io/acme", "https://feeds.example.com/jobs.xml", "https://failing.example.com/jobs")

	runCollect(t, e)

	lr := e.get(t, "/api/status")["lastRun"].(map[string]any)
	if lr["state"] != "done" {
		t.Errorf("lastRun.state: got %v", lr["state"])
	}
	if lr["message"] != "" {
		t.Errorf("lastRun.message: got %v", lr["message"])
	}
}

func TestRunQueuesRecomputationOfFitsYou(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://feeds.example.com/jobs.xml",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(feedXML)}})
	setSources(t, e, "https://feeds.example.com/jobs.xml")
	runCollect(t, e)
	if _, ok := e.r.Last("recompute_fit"); !ok {
		t.Error("recompute_fit was not queued after the run")
	}
}

func TestRunAllFailingEndsFailed(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://failing.example.com/jobs", fetchtest.MapEntry{Err: fetch.ErrNotFound})
	e.fm.Set("https://feeds.example.com/jobs.xml", fetchtest.MapEntry{Err: fetch.ErrTransient})

	setSources(t, e, "https://failing.example.com/jobs", "https://feeds.example.com/jobs.xml")

	runCollect(t, e)

	lr := e.get(t, "/api/status")["lastRun"].(map[string]any)
	if lr["message"] != "Every source failed" {
		t.Errorf("lastRun.message: got %v", lr["message"])
	}
}

func TestRunDeduplicatesPostingsWithinOneRun(t *testing.T) {
	sameURL := "https://feeds.example.com/jobs/shared"
	xmlA := fmt.Sprintf(`<rss version="2.0"><channel><item><title>Shared Job</title><company>Northwind</company><link>%s</link><description>text</description></item></channel></rss>`, sameURL)
	xmlB := fmt.Sprintf(`<rss version="2.0"><channel><item><title>Shared Job</title><company>Northwind</company><link>%s</link><description>other</description></item></channel></rss>`, sameURL)
	e := newEnv(t)
	e.fm.Set("https://feeds.example.com/a.xml", fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(xmlA)}})
	e.fm.Set("https://feeds2.example.com/b.xml", fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(xmlB)}})

	setSources(t, e, "https://feeds.example.com/a.xml", "https://feeds2.example.com/b.xml")

	runCollect(t, e)

	if cat := e.get(t, "/api/status")["catalogue"].(float64); cat != 1 {
		t.Errorf("catalogue: got %v, want 1", cat)
	}
	if reqs := e.fm.Requests(); len(reqs) != 2 {
		t.Errorf("requests: got %d, want 2: %v", len(reqs), reqs)
	}
}

func TestRunTwoBoardsOnSameAPIHostReadSequentially(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/json", Body: []byte(ghBoardResponse)}})
	e.fm.Set("https://boards-api.greenhouse.io/v1/boards/otherco/jobs?content=true",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/json",
			Body: []byte(`{"company":{"name":"OtherCo"},"jobs":[{"id":10,"title":"Dev","absolute_url":"https://boards.greenhouse.io/otherco/jobs/10","content":"<p>x</p>"}]}`)}})

	setSources(t, e, "https://boards.greenhouse.io/acme", "https://boards.greenhouse.io/otherco")

	runCollect(t, e)
	if reqs := e.fm.Requests(); len(reqs) != 2 {
		t.Errorf("requests: got %d, want 2 (one per board): %v", len(reqs), reqs)
	}
}

func TestDueAndCollectDeduplication(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://feeds.example.com/jobs.xml", fetchtest.MapEntry{Err: fetch.ErrNotFound})
	addSource(t, e, "https://feeds.example.com/jobs.xml")

	if !e.col.Due(base) {
		t.Error("Due: want true when no prior run and auto is on")
	}

	e.mutate(t, "POST", "/api/collect", map[string]any{"force": false})
	if e.col.Due(base) {
		t.Error("Due: want false after collect is enqueued (interval not yet elapsed)")
	}
	waitJob(t, e.r)

	id1, _ := e.r.Enqueue(runner.Spec{Kind: "collect"})
	id2, _ := e.r.Enqueue(runner.Spec{Kind: "collect"})
	if id1 != id2 {
		t.Errorf("dedup: second Enqueue returned id %d, want same as first (%d)", id2, id1)
	}

	waitJob(t, e.r)

	e.store.Update(func(c *config.Config) error { c.Collection.Auto = false; return nil })
	if e.col.Due(base) {
		t.Error("Due: want false when auto is off")
	}
}

func TestDueIntervalMeasuredFromLastRunCreated(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://feeds.example.com/jobs.xml", fetchtest.MapEntry{Err: fetch.ErrNotFound})
	addSource(t, e, "https://feeds.example.com/jobs.xml")
	e.store.Update(func(c *config.Config) error {
		c.Collection.Interval = config.Duration(2 * time.Hour)
		return nil
	})
	runCollect(t, e)

	if e.col.Due(base.Add(1 * time.Hour)) {
		t.Error("Due: want false when interval has not elapsed")
	}
	if !e.col.Due(base.Add(2 * time.Hour)) {
		t.Error("Due: want true once interval elapsed")
	}
}

func TestPostCollectForceTrueIgnoresAutoSetting(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://feeds.example.com/jobs.xml", fetchtest.MapEntry{Err: fetch.ErrNotFound})
	addSource(t, e, "https://feeds.example.com/jobs.xml")
	e.store.Update(func(c *config.Config) error {
		c.Collection.Auto = false
		return nil
	})
	_, status := e.mutate(t, "POST", "/api/collect", map[string]any{"force": true})
	if status != 200 {
		t.Fatalf("status: %d", status)
	}
	j := waitJob(t, e.r)
	if j.State != "failed" {
		t.Errorf("state: got %q (ErrNotFound → all failed)", j.State)
	}
}

func TestPostCollectOfflineBlocksAutomatic(t *testing.T) {
	e := newEnvClock(t, func() time.Time { return base }, true)
	e.mutate(t, "POST", "/api/collect", map[string]any{"force": false})
	if len(e.r.Active()) != 0 {
		t.Error("force=false should not enqueue when offline")
	}
	e.mutate(t, "POST", "/api/collect", map[string]any{"force": true})
	waitJob(t, e.r)
}

func TestSourcesListIsInitiallyEmpty(t *testing.T) {
	e := newEnv(t)
	body := e.get(t, "/api/sources")
	if sources := body["sources"].([]any); len(sources) != 0 {
		t.Errorf("sources: got %d, want 0", len(sources))
	}
	if body["auto"] != true {
		t.Errorf("auto: got %v", body["auto"])
	}
	if body["interval"] != "3h" {
		t.Errorf("interval: got %v", body["interval"])
	}
}

func TestAddSourceAppearsInList(t *testing.T) {
	e := newEnv(t)
	body, status := e.mutate(t, "POST", "/api/sources", map[string]any{
		"name": "My Feed",
		"url":  "https://feeds.example.com/jobs.xml",
	})
	if status != 200 {
		t.Fatalf("status: %d body: %v", status, body)
	}
	sources := body["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("sources: got %d, want 1", len(sources))
	}
	s := sources[0].(map[string]any)
	if s["name"] != "My Feed" || s["url"] != "https://feeds.example.com/jobs.xml" || s["enabled"] != true {
		t.Errorf("source: %v", s)
	}
	data, _ := os.ReadFile(filepath.Join(e.dir, "sifty.yaml"))
	if !strings.Contains(string(data), "feeds.example.com") {
		t.Error("settings file should contain new source URL")
	}
}

func TestAddSourceValidationMessages(t *testing.T) {
	e := newEnv(t)
	e.mutate(t, "POST", "/api/sources", map[string]any{"url": "https://feeds.example.com/jobs.xml"})

	check := func(body map[string]any, wantMsg string) {
		t.Helper()
		resp, status := e.mutate(t, "POST", "/api/sources", body)
		if status != 422 {
			t.Errorf("status: got %d, want 422; body: %v", status, resp)
		}
		if !strings.Contains(fmt.Sprint(resp["error"]), wantMsg) {
			t.Errorf("error: got %v, want to contain %q", resp["error"], wantMsg)
		}
	}

	check(map[string]any{"url": "not-a-url"}, "Enter an http(s) address.")
	check(map[string]any{"url": "https://feeds.example.com/jobs.xml"}, "This source is already in the list.")
	check(map[string]any{"name": strings.Repeat("x", 101), "url": "https://other.example.com/jobs.xml"}, "The name is at most 100 characters.")
}

func TestSourceCRUD(t *testing.T) {
	e := newEnv(t)
	_, s1 := e.mutate(t, "PATCH", "/api/sources", map[string]any{"key": "https://no-such.example.com"})
	_, s2 := e.mutate(t, "DELETE", "/api/sources", map[string]any{"key": "https://no-such.example.com"})
	if s1 != 404 || s2 != 404 {
		t.Errorf("PATCH/DELETE unknown key: got %d/%d, want 404/404", s1, s2)
	}
	body, _ := e.mutate(t, "POST", "/api/sources", map[string]any{"url": "https://feeds.example.com/jobs.xml"})
	key := body["sources"].([]any)[0].(map[string]any)["key"].(string)
	body, status := e.mutate(t, "PATCH", "/api/sources", map[string]any{"key": key, "enabled": false})
	if status != 200 {
		t.Fatalf("PATCH status: %d", status)
	}
	if body["sources"].([]any)[0].(map[string]any)["enabled"] != false {
		t.Error("source should be disabled after PATCH")
	}
	body, status = e.mutate(t, "DELETE", "/api/sources", map[string]any{"key": key})
	if status != 200 {
		t.Fatalf("DELETE status: %d", status)
	}
	if len(body["sources"].([]any)) != 0 {
		t.Error("source should be gone after DELETE")
	}
}

func TestPutScheduleValidAndInvalid(t *testing.T) {
	e := newEnv(t)

	body, status := e.mutate(t, "PUT", "/api/sources/schedule", map[string]any{"auto": false, "interval": "1h"})
	if status != 200 {
		t.Fatalf("valid schedule: status %d body %v", status, body)
	}
	if body["auto"] != false || body["interval"] != "1h" {
		t.Errorf("schedule: got auto=%v interval=%v", body["auto"], body["interval"])
	}

	body, status = e.mutate(t, "PUT", "/api/sources/schedule", map[string]any{"auto": true, "interval": "badval"})
	if status != 422 {
		t.Fatalf("bad interval: status %d", status)
	}
	if body["error"] != "The interval must be between 15 minutes and 7 days." {
		t.Errorf("bad interval: got %v", body["error"])
	}

	body, status = e.mutate(t, "PUT", "/api/sources/schedule", map[string]any{"auto": true, "interval": "10m"})
	if status != 422 {
		t.Fatalf("out-of-range interval: status %d", status)
	}
	if !strings.Contains(fmt.Sprint(body["error"]), "The interval must be between 15 minutes and 7 days.") {
		t.Errorf("error message: %v", body["error"])
	}
}

func TestStatusCatalogueCounterIncreasesAfterRun(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://feeds.example.com/jobs.xml",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(feedXML)}})
	addSource(t, e, "https://feeds.example.com/jobs.xml")

	before := e.get(t, "/api/status")["catalogue"].(float64)
	runCollect(t, e)
	after := e.get(t, "/api/status")["catalogue"].(float64)
	if after <= before {
		t.Errorf("catalogue should increase after run; before=%v after=%v", before, after)
	}
	c1 := e.get(t, "/api/status")["catalogue"].(float64)
	if c2 := e.get(t, "/api/status")["catalogue"].(float64); c1 != c2 {
		t.Errorf("catalogue changed without a run: %v → %v", c1, c2)
	}
	if e.get(t, "/api/status")["collecting"] != nil {
		t.Error("collecting should be nil after run")
	}
}

func statusText(t *testing.T, srcs []map[string]any, url string) string {
	t.Helper()
	for _, s := range srcs {
		if s["url"] == url {
			if s["status"] == nil {
				t.Errorf("source %q: status is nil", url)
				return ""
			}
			return s["status"].(map[string]any)["text"].(string)
		}
	}
	t.Errorf("source %q not found", url)
	return ""
}

func sourcesOf(body map[string]any) []map[string]any {
	raw := body["sources"].([]any)
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i] = r.(map[string]any)
	}
	return out
}

func TestRunWritesSourceRunRowsForEachOutcome(t *testing.T) {
	e := newEnv(t)

	e.fm.Set("https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/json", Body: []byte(ghBoardResponse)}})
	e.fm.Set("https://feeds.example.com/empty.xml",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(emptyFeedXML)}})
	e.fm.Set("https://blocked.example.com/jobs",
		fetchtest.MapEntry{Err: fetch.ErrBlocked})
	e.fm.Set("https://noparse.example.com/jobs",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "text/html", Body: []byte(badPageHTML)}})

	setSources(t, e, "https://boards.greenhouse.io/acme", "https://feeds.example.com/empty.xml", "https://blocked.example.com/jobs", "https://noparse.example.com/jobs")

	runCollect(t, e)

	srcs := sourcesOf(e.get(t, "/api/sources"))
	if len(srcs) != 4 {
		t.Fatalf("sources: got %d, want 4", len(srcs))
	}

	acme := statusText(t, srcs, "https://boards.greenhouse.io/acme")
	if !strings.HasPrefix(acme, "Last success") {
		t.Errorf("ok source text: got %q, want prefix 'Last success'", acme)
	}
	if !strings.HasSuffix(acme, "2 seen · 2 new") {
		t.Errorf("ok source text counts: got %q", acme)
	}
	statusText(t, srcs, "https://feeds.example.com/empty.xml")
	if text := statusText(t, srcs, "https://blocked.example.com/jobs"); !strings.Contains(text, "Blocked by site") {
		t.Errorf("blocked source text: got %q, want 'Blocked by site'", text)
	}
	if text := statusText(t, srcs, "https://noparse.example.com/jobs"); !strings.Contains(text, "Nothing recognised on the page") {
		t.Errorf("noparse source text: got %q, want 'Nothing recognised on the page'", text)
	}
}

func TestSourceStatusErrorText(t *testing.T) {
	cases := []struct {
		name         string
		firstOK      bool
		fetchErr     error
		wantContains string
		wantHasPrior bool
	}{
		{
			name:         "not found without prior success",
			fetchErr:     fetch.ErrNotFound,
			wantContains: "Not found",
		},
		{
			name:         "blocked after prior success",
			firstOK:      true,
			fetchErr:     fetch.ErrBlocked,
			wantContains: "Blocked by site",
			wantHasPrior: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			addSource(t, e, "https://feeds.example.com/jobs.xml")
			if tc.firstOK {
				e.fm.Set("https://feeds.example.com/jobs.xml",
					fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(feedXML)}})
				runCollect(t, e)
				srcs := sourcesOf(e.get(t, "/api/sources"))
				text := srcs[0]["status"].(map[string]any)["text"].(string)
				if !strings.HasPrefix(text, "Last success") {
					t.Fatalf("after success: text = %q, want prefix 'Last success'", text)
				}
			}
			e.fm.Set("https://feeds.example.com/jobs.xml", fetchtest.MapEntry{Err: tc.fetchErr})
			runCollect(t, e)
			srcs := sourcesOf(e.get(t, "/api/sources"))
			text := srcs[0]["status"].(map[string]any)["text"].(string)
			if !strings.Contains(text, tc.wantContains) {
				t.Errorf("text = %q, want to contain %q", text, tc.wantContains)
			}
			if tc.wantHasPrior && !strings.Contains(text, "last success") {
				t.Errorf("text = %q, want to contain 'last success'", text)
			}
		})
	}
}

func TestSourceStatusNoVacanciesIn30Days(t *testing.T) {
	var mu sync.Mutex
	nowTime := base
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return nowTime }

	e := newEnvClock(t, now, false)
	e.fm.Set("https://feeds.example.com/empty.xml",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(emptyFeedXML)}})
	addSource(t, e, "https://feeds.example.com/empty.xml")

	runCollect(t, e)

	mu.Lock()
	nowTime = base.Add(31 * 24 * time.Hour)
	mu.Unlock()

	runCollect(t, e)

	srcs := sourcesOf(e.get(t, "/api/sources"))
	text := srcs[0]["status"].(map[string]any)["text"].(string)
	if text != "No vacancies in 30 days" {
		t.Errorf("30-day rule: text = %q, want 'No vacancies in 30 days'", text)
	}
}

func TestAttentionThresholdAndCount(t *testing.T) {
	t.Run("triggers after three failures not two", func(t *testing.T) {
		e := newEnv(t)
		e.fm.Set("https://feeds.example.com/jobs.xml", fetchtest.MapEntry{Err: fetch.ErrBlocked})
		addSource(t, e, "https://feeds.example.com/jobs.xml")
		for run := 1; run <= 3; run++ {
			runCollect(t, e)
			srcs := sourcesOf(e.get(t, "/api/sources"))
			got := srcs[0]["status"].(map[string]any)["attention"].(bool)
			want := run >= 3
			if got != want {
				t.Errorf("after %d runs: attention = %v, want %v", run, got, want)
			}
		}
	})

	t.Run("count and text with two sources, singular after disable", func(t *testing.T) {
		e := newEnv(t)
		e.fm.Set("https://feeds.example.com/a.xml", fetchtest.MapEntry{Err: fetch.ErrBlocked})
		e.fm.Set("https://feeds2.example.com/b.xml", fetchtest.MapEntry{Err: fetch.ErrBlocked})
		setSources(t, e, "https://feeds.example.com/a.xml", "https://feeds2.example.com/b.xml")
		for i := 0; i < 3; i++ {
			runCollect(t, e)
		}
		st := e.get(t, "/api/status")
		att := st["attention"].(map[string]any)
		if att["count"].(float64) != 2 {
			t.Errorf("attention.count: got %v, want 2", att["count"])
		}
		if att["text"] != "2 sources need attention" {
			t.Errorf("attention.text: got %v, want '2 sources need attention'", att["text"])
		}
		e.store.Update(func(c *config.Config) error {
			c.Sources[1].Enabled = false
			return nil
		})
		st = e.get(t, "/api/status")
		att = st["attention"].(map[string]any)
		if att["count"].(float64) != 1 {
			t.Errorf("after disable: attention.count: got %v, want 1", att["count"])
		}
		if att["text"] != "1 source needs attention" {
			t.Errorf("after disable: attention.text: got %v", att["text"])
		}
	})
}

func htmlListing(u string) []byte {
	return []byte(`<html><body><a href="` + u + `">Senior Engineer</a></body></html>`)
}

func companyFeedListing(u string) []byte {
	return []byte(`<rss version="2.0"><channel><item><title>Senior Engineer</title><company>Northwind</company><link>` + u + `</link></item></channel></rss>`)
}

func TestRunDetailFetchesTextForNewPostingWithoutText(t *testing.T) {
	e := newEnv(t)
	const vacURL = "https://generic.example.com/jobs/1"
	e.fm.Set("https://generic.example.com/jobs", fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "text/html", Body: htmlListing(vacURL)}})
	e.fm.Set(vacURL, fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "text/html", Body: []byte(`<html><head><script type="application/ld+json">{"@type":"JobPosting","title":"Senior Engineer","hiringOrganization":{"name":"Acme"},"url":"https://generic.example.com/jobs/1","description":"<p>Full text.</p>"}</script></head></html>`)}})
	addSource(t, e, "https://generic.example.com/jobs")
	runCollect(t, e)
	if n := len(e.fm.Requests()); n != 2 {
		t.Errorf("requests: got %d, want 2 (listing+detail)", n)
	}
	var state string
	if e.db.QueryRow(`SELECT text_state FROM vacancy WHERE url = ?`, vacURL).Scan(&state); state != "full" {
		t.Errorf("text_state: got %q, want full", state)
	}
}

func TestRunDetailSkipsPostingWithText(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/json", Body: []byte(ghBoardResponse)}})
	addSource(t, e, "https://boards.greenhouse.io/acme")
	runCollect(t, e)
	if n := len(e.fm.Requests()); n != 1 {
		t.Errorf("requests: got %d, want 1 (listing only, no detail)", n)
	}
}

func TestRunDetailSkipsWithin7DaysAndRetriesAfter(t *testing.T) {
	var mu sync.Mutex
	nowTime := base
	e := newEnvClock(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return nowTime }, false)
	const listURL, vacURL = "https://generic.example.com/jobs", "https://generic.example.com/jobs/2"
	e.fm.Set(listURL, fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: companyFeedListing(vacURL)}})
	e.fm.Set(vacURL, fetchtest.MapEntry{Err: fetch.ErrNotFound})
	addSource(t, e, listURL)
	run := func(want int) {
		t.Helper()
		runCollect(t, e)
		if n := len(e.fm.Requests()); n != want {
			t.Errorf("requests: got %d, want %d", n, want)
		}
	}
	run(2)
	mu.Lock()
	nowTime = base.Add(24 * time.Hour)
	mu.Unlock()
	run(3)
	mu.Lock()
	nowTime = base.Add(8 * 24 * time.Hour)
	mu.Unlock()
	run(5)
}

func TestRunRemovedAddressGetsNoRequest(t *testing.T) {
	e := newEnv(t)
	const vacURL = "https://generic.example.com/jobs/3"
	e.fm.Set("https://generic.example.com/jobs", fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "text/html", Body: htmlListing(vacURL)}})
	e.db.Exec(`INSERT INTO tombstone (key) VALUES ('url:` + vacURL + `')`)
	addSource(t, e, "https://generic.example.com/jobs")
	runCollect(t, e)
	if n := len(e.fm.Requests()); n != 1 {
		t.Errorf("requests: got %d, want 1 (listing only)", n)
	}
}

func TestRunBlockedDetailSavesPostingAsBlocked(t *testing.T) {
	e := newEnv(t)
	const vacURL = "https://generic.example.com/jobs/4"
	e.fm.Set("https://generic.example.com/jobs", fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: companyFeedListing(vacURL)}})
	e.fm.Set(vacURL, fetchtest.MapEntry{Err: fetch.ErrBlocked})
	addSource(t, e, "https://generic.example.com/jobs")
	runCollect(t, e)
	var state, reqAt string
	if e.db.QueryRow(`SELECT text_state, detail_requested_at FROM vacancy WHERE url = ?`, vacURL).Scan(&state, &reqAt); state != "blocked" || reqAt != "" {
		t.Errorf("text_state=%q detail_requested_at=%q, want blocked and empty", state, reqAt)
	}
}

func TestSourceRunRowDeletedWithJob(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://feeds.example.com/jobs.xml",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(feedXML)}})
	addSource(t, e, "https://feeds.example.com/jobs.xml")

	j := runCollect(t, e)

	var cnt int
	if e.db.QueryRow(`SELECT COUNT(*) FROM source_run WHERE job_id=?`, j.ID).Scan(&cnt); cnt == 0 {
		t.Fatal("source_run: expected a row but found none")
	}

	e.db.Exec(`DELETE FROM job WHERE id=?`, j.ID)
	if e.db.QueryRow(`SELECT COUNT(*) FROM source_run WHERE job_id=?`, j.ID).Scan(&cnt); cnt != 0 {
		t.Errorf("source_run row should be deleted with its job, got %d rows", cnt)
	}
}

func checkExclusionRun(t *testing.T, e *env, wantVacancies int) {
	t.Helper()
	var cnt int
	if e.db.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&cnt); cnt != wantVacancies {
		t.Errorf("catalog: got %d vacancies, want %d", cnt, wantVacancies)
	}
	var excl int
	if e.db.QueryRow(`SELECT excluded FROM source_run ORDER BY job_id DESC LIMIT 1`).Scan(&excl); excl != 1 {
		t.Errorf("source_run.excluded: got %d, want 1", excl)
	}
}

const exclusionFeed = `<rss version="2.0"><channel>
<item><title>Go Developer</title><link>https://feeds.example.com/jobs/excl1</link></item>
</channel></rss>`

const exclusionDetailPage = `<html><head><script type="application/ld+json">` +
	`{"@type":"JobPosting","title":"Go Developer","hiringOrganization":{"name":"ExclCo"},"description":"<p>text</p>"}` +
	`</script></head><body></body></html>`

func TestCollect_ExcludedBeforeDetail(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/json", Body: []byte(ghBoardResponse)}})
	addSource(t, e, "https://boards.greenhouse.io/acme")

	e.db.Exec(`INSERT INTO exclusion (kind,value,norm,profile_id,created) VALUES ('word','engineer','engineer',0,datetime('now'))`)

	runCollect(t, e)
	checkExclusionRun(t, e, 2)
}

func TestCollect_ExcludedAfterDetail(t *testing.T) {
	e := newEnv(t)
	e.fm.Set("https://feeds.example.com/excl.xml",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(exclusionFeed)}})
	e.fm.Set("https://feeds.example.com/jobs/excl1",
		fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "text/html", Body: []byte(exclusionDetailPage)}})
	addSource(t, e, "https://feeds.example.com/excl.xml")

	e.db.Exec(`INSERT INTO exclusion (kind,value,norm,profile_id,created) VALUES ('company','ExclCo','exclco',0,datetime('now'))`)

	runCollect(t, e)
	checkExclusionRun(t, e, 1)
}

const (
	noEmployerListURL = "https://feeds.example.com/openings.xml"
	noEmployerURL     = "https://feeds.example.com/openings/101"
	pageNamesEmployer = `<html><head><script type="application/ld+json">{"@type":"JobPosting","title":"Data Engineer","hiringOrganization":{"name":"Harbor Works"},"description":"<p>Page text.</p>"}</script></head></html>`
	pageNamesNone     = `<html><head><title>Data Engineer</title></head><body><p>Page text.</p></body></html>`
)

func openingsFeed(company string) fetchtest.MapEntry {
	co := ""
	if company != "" {
		co = "<company>" + company + "</company>"
	}
	body := `<rss version="2.0"><channel><item><title>Data Engineer</title>` + co +
		`<link>` + noEmployerURL + `</link><description>Listing text</description></item></channel></rss>`
	return fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(body)}}
}

func TestCollect_PostingWithoutEmployerIsStoredOnlyWhenAnEmployerIsFound(t *testing.T) {
	stored := base.Add(-72 * time.Hour)
	later := base.Add(48 * time.Hour)
	pageNames := fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "text/html", Body: []byte(pageNamesEmployer)}}
	pageNone := fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "text/html", Body: []byte(pageNamesNone)}}
	pageGone := fetchtest.MapEntry{Err: fetch.ErrNotFound}

	for _, tc := range []struct {
		name         string
		before       string
		listing      string
		page         fetchtest.MapEntry
		wantRequests int
		wantStored   int
		wantCompany  string
		wantRefused  int
		wantExcluded int
		wantLastSeen time.Time
	}{
		{"new, page names the employer", "", "", pageNames, 2, 1, "Harbor Works", 0, 0, later},
		{"new, page names none", "", "", pageNone, 2, 0, "", 1, 0, time.Time{}},
		{"new, page cannot be read", "", "", pageGone, 2, 0, "", 1, 0, time.Time{}},
		{"stored with employer, listing names none", "with", "", pageGone, 1, 1, "Harbor Works", 0, 0, later},
		{"stored without employer, page names none", "without", "", pageNone, 2, 1, "", 1, 0, stored},
		{"stored without employer, page names one", "without", "", pageNames, 2, 1, "Harbor Works", 0, 0, later},
		{"new, no employer and an exclusion hides it", "excluded", "", pageNone, 1, 0, "", 0, 1, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			now := base
			e := newEnvClock(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return now }, false)
			addSource(t, e, noEmployerListURL)
			switch tc.before {
			case "with":
				e.fm.Set(noEmployerListURL, openingsFeed("Harbor Works"))
				runCollect(t, e)
			case "excluded":
				if _, err := e.db.Exec(`INSERT INTO exclusion (kind,value,norm,profile_id,created) VALUES ('word','engineer','engineer',0,?)`,
					stored.Format(time.RFC3339)); err != nil {
					t.Fatal(err)
				}
			case "without":
				res, err := e.db.Exec(`INSERT INTO vacancy (identity, title, company, url, first_seen, last_seen, text_state)
					VALUES ('|data engineer', 'Data Engineer', '', ?, ?, ?, 'summary')`,
					noEmployerURL, stored.Format(time.RFC3339), stored.Format(time.RFC3339))
				if err != nil {
					t.Fatal(err)
				}
				id, _ := res.LastInsertId()
				if _, err := e.db.Exec(`INSERT INTO vacancy_source (vacancy_id, source, url, seen_at) VALUES (?, 'feeds.example.com', ?, ?)`,
					id, noEmployerURL, stored.Format(time.RFC3339)); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			now = later
			mu.Unlock()
			e.fm.Set(noEmployerListURL, openingsFeed(tc.listing))
			e.fm.Set(noEmployerURL, tc.page)
			requestsBefore := len(e.fm.Requests())
			runCollect(t, e)

			if got := len(e.fm.Requests()) - requestsBefore; got != tc.wantRequests {
				t.Errorf("requests in the run: got %d, want %d", got, tc.wantRequests)
			}
			var count int
			e.db.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&count)
			if count != tc.wantStored {
				t.Errorf("vacancies stored: got %d, want %d", count, tc.wantStored)
			}
			if tc.wantStored > 0 {
				var company, lastSeen string
				if err := e.db.QueryRow(`SELECT company, last_seen FROM vacancy`).Scan(&company, &lastSeen); err != nil {
					t.Fatal(err)
				}
				if company != tc.wantCompany {
					t.Errorf("company: got %q, want %q", company, tc.wantCompany)
				}
				if want := tc.wantLastSeen.UTC().Format(time.RFC3339); lastSeen != want {
					t.Errorf("last_seen: got %q, want %q", lastSeen, want)
				}
			}
			if tc.wantRefused > 0 && tc.wantStored > 0 {
				var reqAt string
				e.db.QueryRow(`SELECT detail_requested_at FROM vacancy`).Scan(&reqAt)
				if want := later.UTC().Format(time.RFC3339); reqAt != want {
					t.Errorf("detail_requested_at: got %q, want %q", reqAt, want)
				}
			}
			var refused, excluded int
			e.db.QueryRow(`SELECT refused, excluded FROM source_run ORDER BY job_id DESC LIMIT 1`).Scan(&refused, &excluded)
			if refused != tc.wantRefused {
				t.Errorf("refused: got %d, want %d", refused, tc.wantRefused)
			}
			if excluded != tc.wantExcluded {
				t.Errorf("excluded: got %d, want %d", excluded, tc.wantExcluded)
			}
			srcs := e.get(t, "/api/sources")["sources"].([]any)
			text := srcs[0].(map[string]any)["status"].(map[string]any)["text"].(string)
			if has := strings.Contains(text, fmt.Sprintf("%d without an employer", tc.wantRefused)); has != (tc.wantRefused > 0) {
				t.Errorf("status text %q: count shown is %v, want %v", text, has, tc.wantRefused > 0)
			}
		})
	}
}

const ageListURL = "https://feeds.example.com/age.xml"

var ageURLs = struct{ old, recent, undated string }{
	"https://feeds.example.com/jobs/old", "https://feeds.example.com/jobs/recent", "https://feeds.example.com/jobs/undated",
}

func ageFeed(oldTitle string) fetchtest.MapEntry {
	item := func(title, link, date string) string {
		if date != "" {
			date = "<pubDate>" + date + "</pubDate>"
		}
		return `<item><title>` + title + `</title><company>Northwind</company><link>` + link + `</link>` + date + `</item>`
	}
	body := `<rss version="2.0"><channel>` +
		item(oldTitle, ageURLs.old, "Tue, 10 Jun 2025 09:00:00 +0000") +
		item("Recent Role", ageURLs.recent, "Sat, 20 Dec 2025 09:00:00 +0000") +
		item("Undated Role", ageURLs.undated, "") +
		`</channel></rss>`
	return fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/xml", Body: []byte(body)}}
}

func ageEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.fm.Set(ageListURL, ageFeed("Old Role"))
	for _, u := range []string{ageURLs.old, ageURLs.recent, ageURLs.undated} {
		e.fm.Set(u, fetchtest.MapEntry{Err: fetch.ErrNotFound})
	}
	addSource(t, e, ageListURL)
	return e
}

func (e *env) setAgeLimit(t *testing.T, months any) {
	t.Helper()
	if _, status := e.mutate(t, "PUT", "/api/sources/schedule", map[string]any{"auto": true, "interval": "3h", "max_age_months": months}); status != 200 {
		t.Fatalf("set age limit %v: status %d", months, status)
	}
}

func (e *env) storedURLs(t *testing.T) map[string]bool {
	t.Helper()
	rows, err := e.db.Query(`SELECT url FROM vacancy`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var u string
		rows.Scan(&u)
		out[u] = true
	}
	return out
}

func TestCollect_PostingOlderThanTheAgeLimitIsNotStoredAndItsPageIsNotRequested(t *testing.T) {
	e := ageEnv(t)
	e.setAgeLimit(t, 3)
	runCollect(t, e)

	stored := e.storedURLs(t)
	if stored[ageURLs.old] || !stored[ageURLs.recent] || !stored[ageURLs.undated] {
		t.Errorf("stored: %v, want the recent and the undated posting only", stored)
	}
	for _, r := range e.fm.Requests() {
		if r == ageURLs.old {
			t.Errorf("the page of the old posting was requested")
		}
	}
	if n := len(e.fm.Requests()); n != 3 {
		t.Errorf("requests: got %d, want 3 (listing and two pages)", n)
	}
}

func TestCollect_AgeLimitLeavesStoredVacanciesUpdatedAndLeavesNoTombstone(t *testing.T) {
	e := ageEnv(t)
	runCollect(t, e)
	e.fm.Set(ageListURL, ageFeed("Old Role Renamed"))
	e.setAgeLimit(t, 1)
	runCollect(t, e)
	if n := len(e.storedURLs(t)); n != 3 {
		t.Errorf("after tightening the limit: stored %d, want 3", n)
	}
	var title string
	e.db.QueryRow(`SELECT title FROM vacancy WHERE url = ?`, ageURLs.old).Scan(&title)
	if title != "Old Role Renamed" {
		t.Errorf("a vacancy already stored stopped being updated: title %q", title)
	}

	fresh := ageEnv(t)
	fresh.setAgeLimit(t, 1)
	runCollect(t, fresh)
	if n := len(fresh.storedURLs(t)); n != 2 {
		t.Fatalf("with the limit: stored %d, want 2", n)
	}
	fresh.setAgeLimit(t, nil)
	runCollect(t, fresh)
	if n := len(fresh.storedURLs(t)); n != 3 {
		t.Errorf("after the limit is cleared: stored %d, want 3", n)
	}
}

func TestPutScheduleAgeLimit(t *testing.T) {
	e := newEnv(t)
	put := func(months any) (map[string]any, int) {
		return e.mutate(t, "PUT", "/api/sources/schedule", map[string]any{"auto": true, "interval": "3h", "max_age_months": months})
	}
	if body, status := put(6); status != 200 || body["max_age_months"] != float64(6) {
		t.Fatalf("limit 6: status %d body %v", status, body)
	}
	if got := e.get(t, "/api/sources")["max_age_months"]; got != float64(6) {
		t.Errorf("GET after PUT: %v, want 6", got)
	}
	for _, bad := range []any{0, -2, 1.5, 1201} {
		body, status := put(bad)
		if status != 422 {
			t.Errorf("limit %v: status %d, want 422", bad, status)
			continue
		}
		if body["error"] != "The age limit is a whole number of months, from 1 to 1200, or empty for no limit." {
			t.Errorf("limit %v: error %v", bad, body["error"])
		}
	}
	if got := e.get(t, "/api/sources")["max_age_months"]; got != float64(6) {
		t.Errorf("refused values changed the limit: %v", got)
	}
	if body, status := put(1200); status != 200 || body["max_age_months"] != float64(1200) {
		t.Errorf("limit 1200: status %d body %v", status, body)
	}
	if body, status := put(nil); status != 200 || body["max_age_months"] != nil {
		t.Errorf("empty limit: status %d body %v", status, body)
	}
}
