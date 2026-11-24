package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func closedEndpoint(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr
}

func TestServerLogsHideHeldKeysCarriedByAProviderError(t *testing.T) {
	const envKey = "env-key-0123456789"
	const fileKey = "file-key-0123456789"
	t.Setenv("BOX_KEY", envKey)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("OTHER_KEY="+fileKey+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("providers:\n  box: {type: openai, model: m, cost: local, endpoint: %q, key_env: BOX_KEY}\nwork: {reading: box}\n", closedEndpoint(t)+"/"+fileKey+"/"+envKey)
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	v := insertFullVacancy(t, d, "https://jobs.example.com/mask", "Backend Engineer", "Stackworks")
	ts := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.Exec(`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
		VALUES ('read_vacancy',?,0,'queued',0,?,'{}','',NULL,?,?)`, fmt.Sprintf("%d/1", v), ts, ts, ts); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	d.Close()

	var stderr lockedBuffer
	_, cancel := startServerWithStderr(t, dir, false, &stderr)
	logPath := filepath.Join(dir, logDir, logFileName)
	waitFor(t, 10*time.Second, func() bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), "runner: job failed") && strings.Contains(stderr.String(), "runner: job failed")
	})
	cancel()

	file, _ := os.ReadFile(logPath)
	for name, text := range map[string]string{"sifty.log": string(file), "standard error": stderr.String()} {
		if strings.Contains(text, envKey) || strings.Contains(text, fileKey) {
			t.Errorf("%s holds a key", name)
		}
		if !strings.Contains(text, "/***/***/") {
			t.Errorf("%s does not show the masked address of the failed call", name)
		}
	}
}

func seedOldLogs(t *testing.T, dir, first, second string) (string, string) {
	t.Helper()
	logs := filepath.Join(dir, logDir)
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	rotated, current := filepath.Join(logs, logFileName+".1"), filepath.Join(logs, logFileName)
	for path, text := range map[string]string{rotated: first, current: second} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return rotated, current
}

func TestOldLogLinesLoseHeldKeysAtStart(t *testing.T) {
	const envKey = "env-key-0123456789"
	const fileKey = "file-key-0123456789"
	t.Setenv("BOX_KEY", envKey)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("OTHER_KEY="+fileKey+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("providers:\n  box: {type: openai, model: m, cost: local, endpoint: %q, key_env: BOX_KEY}\n", closedEndpoint(t))
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	rotated, current := seedOldLogs(t, dir,
		`{"time":"t1","level":"WARN","msg":"call failed","err":"GET /`+fileKey+`/x"}`+"\n"+
			`time=t2 level=ERROR msg="call failed" err="Bearer `+envKey+`"`+"\n"+
			`{"time":"t3","level":"INFO","msg":"plain line"}`+"\n",
		`{"time":"t4","level":"WARN","msg":"`+envKey+` and `+fileKey+`"}`+"\n"+
			`{"time":"t5","level":"INFO","msg":"other line"}`+"\n")

	_, cancel := startServer(t, dir, true)
	cancel()

	for path, want := range map[string]string{
		rotated: `{"time":"t1","level":"WARN","msg":"call failed","err":"GET /***/x"}` + "\n" +
			`time=t2 level=ERROR msg="call failed" err="Bearer ***"` + "\n" +
			`{"time":"t3","level":"INFO","msg":"plain line"}` + "\n",
		current: `{"time":"t4","level":"WARN","msg":"*** and ***"}` + "\n" +
			`{"time":"t5","level":"INFO","msg":"other line"}` + "\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(got)
		if strings.Contains(text, envKey) || strings.Contains(text, fileKey) {
			t.Errorf("%s still holds a key", filepath.Base(path))
		}
		if !strings.HasPrefix(text, want) {
			t.Errorf("%s: old lines are not the scrubbed old lines", filepath.Base(path))
		}
		if path == current && !strings.Contains(strings.TrimPrefix(text, want), "Listening on") {
			t.Errorf("sifty.log: the lines of this start do not follow the old lines")
		}
		if path == rotated && text != want {
			t.Errorf("sifty.log.1: changed beyond the scrub")
		}
	}
	if info, err := os.Stat(rotated); err != nil || info.Mode().Perm() != 0600 {
		t.Errorf("sifty.log.1 mode after scrub: %v, %v", info, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, logDir, "*.scrub-*")); len(left) > 0 {
		t.Errorf("temporary files left in logs: %v", left)
	}
}

func TestOldLogFilesWithoutKeysAreNotRewritten(t *testing.T) {
	t.Setenv("BOX_KEY", "env-key-0123456789")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("OTHER_KEY=file-key-0123456789\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("providers:\n  box: {type: openai, model: m, cost: local, endpoint: %q, key_env: BOX_KEY}\n", closedEndpoint(t))
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	old := `{"time":"t1","level":"INFO","msg":"nothing secret"}` + "\n"
	rotated, current := seedOldLogs(t, dir, old, old)
	past := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(rotated, past, past); err != nil {
		t.Fatal(err)
	}

	_, cancel := startServer(t, dir, true)
	cancel()

	if info, err := os.Stat(rotated); err != nil || !info.ModTime().Equal(past) {
		t.Errorf("sifty.log.1 was written: %v, %v", info, err)
	}
	if got, _ := os.ReadFile(rotated); string(got) != old {
		t.Errorf("sifty.log.1 content changed")
	}
	got, _ := os.ReadFile(current)
	if !strings.HasPrefix(string(got), old) || !strings.Contains(strings.TrimPrefix(string(got), old), "Listening on") {
		t.Errorf("sifty.log should hold the old line and then the lines of this start")
	}
}
