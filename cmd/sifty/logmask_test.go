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
