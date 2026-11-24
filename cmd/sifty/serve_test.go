package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeCreatesDataFolderWithRestrictedPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newdata")
	port, cancel := startServer(t, dir, true)
	defer cancel()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("data dir not created: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("data dir permissions: got %o, want 0700", info.Mode().Perm())
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/api/status: got %d, want 200", resp.StatusCode)
	}
}

func TestSecondServeOnSameFolderIsRefused(t *testing.T) {
	dir := t.TempDir()
	_, cancel := startServer(t, dir, true)
	defer cancel()

	var stderr bytes.Buffer
	ctx := context.Background()
	err := run(ctx, []string{"serve", "--data-dir", dir, "--port", "0"}, io.Discard, &stderr)

	if err == nil {
		t.Fatal("second serve should return an error")
	}
	if !strings.Contains(stderr.String(), "Another Sifty is already running") {
		t.Errorf("second serve: stderr %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), dir) {
		t.Errorf("second serve message should include the path, stderr: %q", stderr.String())
	}
}

func TestConfigDefaultsPrintsValidYAML(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"config", "defaults"}, &stdout, io.Discard); err != nil {
		t.Fatalf("config defaults: %v", err)
	}
	if stdout.Len() == 0 {
		t.Fatal("config defaults: empty output")
	}
}

type recoveredNotice struct {
	File string `json:"file"`
	Text string `json:"text"`
}

func recoveredAtStart(t *testing.T, dir string) []recoveredNotice {
	t.Helper()
	port, cancel := startServer(t, dir, true)
	defer cancel()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	var status struct {
		Recovered []recoveredNotice `json:"recovered"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	return status.Recovered
}

func brokenFileIn(t *testing.T, dir, prefix string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, prefix+".broken-*"))
	if len(matches) != 1 {
		t.Fatalf("want one %s.broken-* file in %s, got %v", prefix, dir, matches)
	}
	return filepath.Base(matches[0])
}

func TestRefusedValueIsPutAsideAndBackupRestoredWithTheSentenceInStatus(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backups", "sifty-20240101T120000.000000000Z.yaml"), []byte("port: 9191\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte("collection:\n  max_age_months: 5000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got := recoveredAtStart(t, dir)
	if len(got) != 1 || got[0].File != "sifty.yaml" {
		t.Fatalf("recovered = %+v", got)
	}
	want := "Your settings file could not be used and was moved to " + brokenFileIn(t, dir, "sifty.yaml") + ". " +
		"The age limit is a whole number of months, from 1 to 1200, or empty for no limit. " +
		"The settings were restored from the backup backups/sifty-20240101T120000.000000000Z.yaml."
	if got[0].Text != want {
		t.Errorf("text = %q\nwant   %q", got[0].Text, want)
	}
}

func TestUnknownConfigKeyIsNamedAndNothingIsMovedOrRestored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte("unknown_top_key: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got := recoveredAtStart(t, dir)
	want := "Your settings file has settings this version does not use; they were ignored: unknown_top_key."
	if len(got) != 1 || got[0].File != "sifty.yaml" || got[0].Text != want {
		t.Errorf("recovered = %+v, want text %q", got, want)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "sifty.yaml.broken-*")); len(matches) != 0 {
		t.Errorf("settings file was moved: %v", matches)
	}
}

func TestSiteDescriptionsWithDuplicatedHostArePutAsideAndSaidSo(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vocab"), 0700); err != nil {
		t.Fatal(err)
	}
	sites := "sites:\n  - host: board.example.com\n    module: feed\n  - host: board.example.com\n    module: links\n"
	if err := os.WriteFile(filepath.Join(dir, "vocab", "sites.yaml"), []byte(sites), 0600); err != nil {
		t.Fatal(err)
	}
	got := recoveredAtStart(t, dir)
	want := "Your site descriptions file vocab/sites.yaml could not be used and was moved to " + brokenFileIn(t, filepath.Join(dir, "vocab"), "sites.yaml") + ". " +
		"The host board.example.com is listed twice. The built-in site descriptions are used."
	if len(got) != 1 || got[0].File != "sites.yaml" || got[0].Text != want {
		t.Errorf("recovered = %+v, want text %q", got, want)
	}
}

func TestPortZeroTakesAFreePortNotDefaultPort(t *testing.T) {
	dir := t.TempDir()
	port, cancel := startServer(t, dir, true)
	defer cancel()

	if port == 7373 {
		t.Error("--port 0 should not listen on the default port 7373")
	}
	if port <= 0 {
		t.Errorf("--port 0 should resolve to a positive port number, got %d", port)
	}
}

func TestReplayFetchesFromFixtureAndLogsUnrecordedAddress(t *testing.T) {
	replayDir := t.TempDir()
	boardBody := `{"company":{"name":"TestCo"},"jobs":[{"id":1,"title":"Engineer","absolute_url":"https://boards.greenhouse.io/testco/jobs/1","content":"<p>Job</p>"}]}`
	if err := os.WriteFile(filepath.Join(replayDir, "board.json"), []byte(boardBody), 0600); err != nil {
		t.Fatal(err)
	}
	index := `{"entries":[{"url":"https://boards-api.greenhouse.io/v1/boards/testco/jobs?content=true","status":200,"content_type":"application/json","body_file":"board.json"}]}`
	if err := os.WriteFile(filepath.Join(replayDir, "index.json"), []byte(index), 0600); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	ctx, cancelCtx := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx, []string{"serve", "--offline", "--replay", replayDir, "--data-dir", dataDir, "--port", "0"},
			pw, io.Discard)
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
	var port int
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
	defer func() { cancelCtx(); <-done; pr.Close() }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	postJSON := func(path, body string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
	}

	postJSON("/api/sources", `{"url":"https://boards.greenhouse.io/testco"}`)
	postJSON("/api/sources", `{"url":"https://feeds.unrecorded.example.com/jobs.xml"}`)

	postJSON("/api/collect", `{"force":true}`)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/status")
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), `"lastRun":{`) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	logPath := filepath.Join(dataDir, "logs", "sifty.log")
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "replay: not recorded") {
		t.Error("log should contain 'replay: not recorded' for the unrecorded address")
	}
	if !strings.Contains(logText, "feeds.unrecorded.example.com") {
		t.Errorf("log should include the unrecorded URL; log:\n%s", logText)
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &stdout, io.Discard); err != nil {
		t.Fatalf("version: %v", err)
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatal("version: empty output")
	}
}

func TestDocumentUploadLimitUsesRealServerWiring(t *testing.T) {
	dir := t.TempDir()
	port, cancel := startServer(t, dir, true)
	defer cancel()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	exactly2MiB := make([]byte, 2*1024*1024)
	for i := range exactly2MiB {
		exactly2MiB[i] = 'a'
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/api/documents?name=big.txt", bytes.NewReader(exactly2MiB))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST 2 MiB: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("file at exactly 2 MiB via real server: status %d, want 200", resp.StatusCode)
	}

	over2MiB := make([]byte, 2*1024*1024+1)
	for i := range over2MiB {
		over2MiB[i] = 'a'
	}
	req2, _ := http.NewRequest(http.MethodPost, base+"/api/documents?name=toobig.txt", bytes.NewReader(over2MiB))
	req2.Header.Set("Content-Type", "text/plain")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST 2 MiB+1: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("file over 2 MiB via real server: status %d, want 413", resp2.StatusCode)
	}
}
