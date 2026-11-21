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
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/collect"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
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
	ctx, cancelCtx := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan struct{})
	args := []string{"serve", "--data-dir", dir, "--port", "0"}
	if offline {
		args = []string{"serve", "--offline", "--data-dir", dir, "--port", "0"}
	}
	go func() {
		defer close(done)
		run(ctx, args, pw, io.Discard)
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
	store, _, err := config.Open(filepath.Join(t.TempDir(), "sifty.yaml"))
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	registerJobKinds(r, d, store, fixedModel{m}, cat, slog.New(slog.NewTextHandler(io.Discard, nil)), false, now)
}

func newRouteServer(t *testing.T, d *db.DB, r *runner.Runner) string {
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
		registerAllRoutes(mux, logger, nil, d, time.Now, nil, nil, nil, nil, t.TempDir(), collect.New(nil, nil, nil, nil, nil, false, nil, logger), r)
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
