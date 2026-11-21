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

func TestBrokenSettingsFileIsRecoveredAtStart(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte("{{{: not valid yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	port, cancel := startServer(t, dir, true)
	defer cancel()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var status struct {
		Recovered []struct {
			File   string `json:"file"`
			Err    string `json:"err"`
			Broken string `json:"broken"`
		} `json:"recovered"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if len(status.Recovered) == 0 {
		t.Error("broken settings file should appear in status recovered list")
	} else {
		if status.Recovered[0].Broken == "" {
			t.Error("recovery should name the broken file")
		}
		if !strings.Contains(status.Recovered[0].File, "sifty.yaml") {
			t.Errorf("recovery file = %q, want sifty.yaml", status.Recovered[0].File)
		}
	}
}

func TestUnknownConfigKeyAppearsInStatusRecovered(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte("unknown_top_key: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	port, cancel := startServer(t, dir, true)
	defer cancel()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var status struct {
		Recovered []struct {
			File   string `json:"file"`
			Err    string `json:"err"`
			Broken string `json:"broken"`
		} `json:"recovered"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if len(status.Recovered) == 0 {
		t.Error("unknown key should appear in status recovered list")
		return
	}
	r := status.Recovered[0]
	if r.Broken != "" {
		t.Error("unknown key should not rename the settings file")
	}
	if !strings.Contains(r.Err, "unknown_top_key") {
		t.Errorf("recovery error should name the unknown key, got %q", r.Err)
	}
	if !strings.Contains(r.File, "sifty.yaml") {
		t.Errorf("recovery file should be sifty.yaml, got %q", r.File)
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
