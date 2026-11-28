package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/collect"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/server"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(fmt.Sprintf("%s/test.db", t.TempDir()), db.Migrations)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func startServer(t *testing.T, dir string, offline bool) (port int, cancel func()) {
	t.Helper()
	return startServerWithStderr(t, dir, offline, io.Discard)
}

func startServerWithStderr(t *testing.T, dir string, offline bool, stderr io.Writer) (port int, cancel func()) {
	t.Helper()
	ctx, cancelCtx := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan struct{})
	args := []string{"serve", "--data-dir", dir, "--port", "0"}
	if offline {
		args = []string{"serve", "--offline", "--data-dir", dir, "--port", "0"}
	}
	go func() {
		defer close(done)
		run(ctx, args, pw, stderr)
		pw.Close()
	}()
	sc := bufio.NewScanner(pr)
	portCh := make(chan int, 1)
	go func() {
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "Listening on http://127.0.0.1:") {
				var p int
				if _, err := fmt.Sscanf(line, "Listening on http://127.0.0.1:%d", &p); err == nil {
					portCh <- p
					return
				}
			}
		}
		portCh <- 0
	}()
	select {
	case p := <-portCh:
		if p == 0 {
			cancelCtx()
			t.Fatal("did not receive 'Listening on' line")
		}
		port = p
	case <-time.After(10 * time.Second):
		cancelCtx()
		t.Fatal("timeout waiting for server to start")
	}
	cancel = func() { cancelCtx(); <-done; pr.Close() }
	return port, cancel
}

type fixedModel struct{ models.Model }

func (f fixedModel) For(string, string, ...int64) models.Model { return f.Model }

func registerKindsWith(t *testing.T, r *runner.Runner, d *db.DB, m models.Model) {
	t.Helper()
	registerKindsAt(t, r, d, m, catalog.New(d, time.Now), time.Now)
}

func registerKindsAt(t *testing.T, r *runner.Runner, d *db.DB, m models.Model, cat *catalog.Catalog, now func() time.Time) {
	t.Helper()
	registerKindsFetching(t, r, d, m, cat, now, fetchtest.NewMap())
}

func registerKindsFetching(t *testing.T, r *runner.Runner, d *db.DB, m models.Model, cat *catalog.Catalog, now func() time.Time, f fetch.Fetcher) {
	t.Helper()
	store, _, err := config.Open(filepath.Join(t.TempDir(), "sifty.yaml"))
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	registerJobKinds(r, d, store, fixedModel{m}, cat, f, slog.New(slog.NewTextHandler(io.Discard, nil)), false, now)
}

func newRouteServer(t *testing.T, d *db.DB, r *runner.Runner) string {
	t.Helper()
	return newRouteServerWithReaders(t, d, r, candidate.Readers{PDF: candidate.ProgramText{Program: filepath.Join(t.TempDir(), "absent")}})
}

func newRouteServerWithReaders(t *testing.T, d *db.DB, r *runner.Runner, readers candidate.Readers) string {
	t.Helper()
	return newRouteServerWithReading(t, d, r, readers, nil)
}

func newRouteServerWithReading(t *testing.T, d *db.DB, r *runner.Runner, readers candidate.Readers, rd *reading) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := server.New(server.Options{
		Version:           "test",
		Port:              port,
		Logger:            logger,
		BinaryUploadPaths: []string{"/api/documents"},
	}, func(mux *http.ServeMux) {
		registerAllRoutes(mux, logger, nil, d, time.Now, nil, nil, nil, enqueueRecomputeFit(d, r, logger, rd), t.TempDir(), readers, collect.New(nil, nil, nil, nil, nil, false, nil, logger), r, nil, filepath.Join(t.TempDir(), logFileName), nil, rd)
	})
	ts := httptest.NewUnstartedServer(h)
	ts.Listener = l
	ts.Start()
	t.Cleanup(ts.Close)
	return ts.URL
}

func apiGet(t *testing.T, base, path string) (map[string]any, int) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}

func apiSend(t *testing.T, method, base, path string, payload any) (map[string]any, int) {
	t.Helper()
	var body io.Reader
	switch v := payload.(type) {
	case string:
		body = strings.NewReader(v)
	case nil:
		body = strings.NewReader("{}")
	default:
		b, _ := json.Marshal(v)
		body = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, base+path, body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}

func waitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within", timeout)
}

func waitForDocState(t *testing.T, base string, docID float64, wantState string, timeout time.Duration) {
	t.Helper()
	waitFor(t, timeout, func() bool {
		body, _ := apiGet(t, base, "/api/documents")
		docs, _ := body["documents"].([]any)
		for _, d := range docs {
			dm, _ := d.(map[string]any)
			if dm["id"] == docID && dm["state"] == wantState {
				return true
			}
		}
		return false
	})
}

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
