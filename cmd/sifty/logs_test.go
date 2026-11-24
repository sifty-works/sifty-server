package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type logsFixture struct {
	dir  string
	base string
	stop func()
}

func logLine(level, msg string, attrs ...string) string {
	line := fmt.Sprintf(`{"time":"2026-11-20T10:00:00+01:00","level":%q,"msg":%q`, level, msg)
	for i := 0; i+1 < len(attrs); i += 2 {
		line += fmt.Sprintf(`,%q:%q`, attrs[i], attrs[i+1])
	}
	return line + "}\n"
}

func writeLog(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	logs := filepath.Join(dir, logDir)
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatalf("make logs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logs, name), []byte(strings.Join(lines, "")), 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func startLogsServe(t *testing.T, dir string, settings string) logsFixture {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(settings), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	port, cancel := startServer(t, dir, true)
	t.Cleanup(cancel)
	return logsFixture{dir: dir, base: fmt.Sprintf("http://127.0.0.1:%d", port)}
}

func getLogs(t *testing.T, base string) logsResp {
	t.Helper()
	body, status := apiGet(t, base, "/api/logs")
	if status != http.StatusOK {
		t.Fatalf("GET /api/logs: status %d", status)
	}
	b, _ := json.Marshal(body)
	var out logsResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	return out
}

func attr(e logEntry, key string) string {
	for _, a := range e.Attrs {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

func TestLogsHoldOnlyWarningsAndErrorsNewestFirstAcrossBothFiles(t *testing.T) {
	dir := t.TempDir()
	var older, newer []string
	for i := 0; i < 150; i++ {
		older = append(older, logLine("ERROR", "older failure", "id", fmt.Sprint(i)), logLine("INFO", "older chatter"))
		newer = append(newer, logLine("WARN", "newer warning", "job", fmt.Sprint(i)), logLine("DEBUG", "newer chatter"))
	}
	older = append([]string{"time=2026-11-01T10:00:00 level=ERROR msg=\"old text line\"\n"}, older...)
	newer = append(newer, "not a log line at all\n")
	writeLog(t, dir, logFileName+".1", older...)
	writeLog(t, dir, logFileName, newer...)
	s := startLogsServe(t, dir, "port: 7373\n")

	entries := getLogs(t, s.base).Entries
	if len(entries) != logEntryLimit {
		t.Fatalf("got %d entries, want %d", len(entries), logEntryLimit)
	}
	for i, e := range entries {
		if e.Level != "WARN" && e.Level != "ERROR" {
			t.Fatalf("entry %d has level %s", i, e.Level)
		}
	}
	if e := entries[0]; e.Message != "newer warning" || attr(e, "job") != "149" || e.Time != "2026-11-20T10:00:00+01:00" {
		t.Errorf("first entry = %+v, want the last warning of sifty.log", e)
	}
	if e := entries[150]; e.Message != "older failure" || attr(e, "id") != "149" {
		t.Errorf("entry 150 = %+v, want the newest failure of sifty.log.1", e)
	}
	if e := entries[199]; attr(e, "id") != "100" {
		t.Errorf("last entry = %+v, want id 100", e)
	}
}

func TestLogsMaskHeldKeysInMessagesAndAttributes(t *testing.T) {
	const envKey = "env-key-0123456789"
	const fileKey = "file-key-0123456789"
	t.Setenv("BOX_KEY", envKey)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("OTHER_KEY="+fileKey+"\nSHORT=abc\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeLog(t, dir, logFileName,
		logLine("ERROR", "call failed with "+envKey, "error", "bad token "+fileKey+" and "+envKey, "note", "abc stays"),
	)
	s := startLogsServe(t, dir, "providers:\n  box: {type: openai, model: m, endpoint: \"http://127.0.0.1:9\", key_env: BOX_KEY}\n")

	entries := getLogs(t, s.base).Entries
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1", entries)
	}
	e := entries[0]
	if e.Message != "call failed with ***" || attr(e, "error") != "bad token *** and ***" || attr(e, "note") != "abc stays" {
		t.Errorf("entry = %+v, want keys masked and a short secret left alone", e)
	}
}

func TestLogsReadOnlyTheEndOfALargeFile(t *testing.T) {
	dir := t.TempDir()
	lines := []string{logLine("ERROR", "first line of the file")}
	filler := logLine("INFO", strings.Repeat("filler ", 40))
	for range (9 << 20) / len(filler) {
		lines = append(lines, filler)
	}
	for i := 0; i < 100; i++ {
		lines = append(lines, logLine("ERROR", "recent failure", "id", fmt.Sprint(i), "detail", strings.Repeat("x", 2000+i)))
		for range 5 {
			lines = append(lines, filler)
		}
	}
	writeLog(t, dir, logFileName, lines...)
	s := startLogsServe(t, dir, "port: 7373\n")

	entries := getLogs(t, s.base).Entries
	if len(entries) != 100 {
		t.Fatalf("got %d entries, want the 100 recent errors and not the first line of the file", len(entries))
	}
	for i, e := range entries {
		id := 99 - i
		if e.Message != "recent failure" || attr(e, "id") != fmt.Sprint(id) || attr(e, "detail") != strings.Repeat("x", 2000+id) {
			t.Fatalf("entry %d = %+v, want the whole error with id %d", i, e, id)
		}
	}
}
