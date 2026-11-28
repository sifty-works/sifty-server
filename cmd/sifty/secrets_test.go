package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const unrelatedSecretLine = "OTHER_KEY=unrelated-value-1234\n"

type keysFixture struct {
	base, dir string
	mu        sync.Mutex
	auth      []string
}

func (f *keysFixture) lastAuth() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth[len(f.auth)-1]
}

func startKeysServe(t *testing.T, secretsText string) *keysFixture {
	t.Helper()
	t.Setenv("BOX_KEY", "")
	f := &keysFixture{dir: t.TempDir()}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(fake.Close)
	settings := fmt.Sprintf("providers:\n  box: {type: openai, model: m, cost: local, endpoint: %q, key_env: BOX_KEY}\n  plain: {type: ollama, model: m, cost: local}\n", fake.URL+"/v1")
	if err := os.WriteFile(filepath.Join(f.dir, "sifty.yaml"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	if secretsText != "" {
		if err := os.WriteFile(f.secretsPath(), []byte(secretsText), 0644); err != nil {
			t.Fatal(err)
		}
	}
	port, cancel := startServer(t, f.dir, false)
	t.Cleanup(cancel)
	f.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	return f
}

func (f *keysFixture) secretsPath() string { return filepath.Join(f.dir, "secrets.env") }

func (f *keysFixture) call(t *testing.T, method, path string, payload any) (string, int) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, f.base+path, body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode
}

func (f *keysFixture) put(t *testing.T, name, value string) (string, int) {
	t.Helper()
	return f.call(t, "PUT", "/api/secrets/"+name, map[string]string{"value": value})
}

func (f *keysFixture) check(t *testing.T) string {
	t.Helper()
	body, status := f.call(t, "POST", "/api/providers/box/check", nil)
	if status != http.StatusOK || !strings.Contains(body, `"ok"`) {
		t.Fatalf("check: status %d, body %s", status, body)
	}
	return f.lastAuth()
}

func (f *keysFixture) appendLog(t *testing.T, line string) {
	t.Helper()
	logs := filepath.Join(f.dir, logDir)
	os.MkdirAll(logs, 0700)
	file, err := os.OpenFile(filepath.Join(logs, logFileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	file.WriteString(line)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSavedKeyLandsInSecretsFileOnly(t *testing.T) {
	const key = "sk-invented-0123456789"
	f := startKeysServe(t, unrelatedSecretLine)
	settingsBefore := readFile(t, filepath.Join(f.dir, "sifty.yaml"))

	putBody, status := f.put(t, "BOX_KEY", "  "+key+"  ")
	if status != http.StatusOK {
		t.Fatalf("PUT: status %d, body %s", status, putBody)
	}

	if got := readFile(t, f.secretsPath()); got != unrelatedSecretLine+"BOX_KEY="+key+"\n" {
		t.Errorf("secrets.env = %q, want the unrelated line kept and the trimmed key added", got)
	}
	if info, err := os.Stat(f.secretsPath()); err != nil || info.Mode().Perm() != 0600 {
		t.Errorf("secrets.env mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
	if got := readFile(t, filepath.Join(f.dir, "sifty.yaml")); got != settingsBefore {
		t.Errorf("settings file changed:\n%s", got)
	}
	listBody, _ := f.call(t, "GET", "/api/secrets", nil)
	modelsBody, _ := f.call(t, "GET", "/api/models", nil)
	for name, body := range map[string]string{"PUT": putBody, "GET /api/secrets": listBody, "GET /api/models": modelsBody} {
		if strings.Contains(body, key) {
			t.Errorf("%s response holds the key: %s", name, body)
		}
	}
	for _, want := range []string{`"name":"BOX_KEY"`, `"providers":["box"]`, `"set":true`, `"last4":"6789"`, `"source":"file"`} {
		if !strings.Contains(listBody, want) {
			t.Errorf("GET /api/secrets = %s, want %s", listBody, want)
		}
	}
	if strings.Contains(listBody, "OTHER_KEY") || strings.Contains(listBody, "plain") {
		t.Errorf("GET /api/secrets = %s, want only the keys providers name", listBody)
	}
}

func TestSavedKeyIsUsedAndMaskedAtOnceWithoutRestart(t *testing.T) {
	const first, second = "sk-first-0123456789", "sk-second-0123456789"
	f := startKeysServe(t, "")
	if got := f.check(t); got != "" {
		t.Fatalf("Authorization before any key = %q, want none", got)
	}

	for _, key := range []string{first, second} {
		if _, status := f.put(t, "BOX_KEY", key); status != http.StatusOK {
			t.Fatalf("PUT %s: status %d", key, status)
		}
		if got := f.check(t); got != "Bearer "+key {
			t.Errorf("Authorization after saving %s = %q, want the new key", key, got)
		}
		f.appendLog(t, logLine("ERROR", "call failed with "+key, "error", "bad token "+key))
		body, _ := f.call(t, "GET", "/api/logs", nil)
		if strings.Contains(body, key) || !strings.Contains(body, "call failed with ***") {
			t.Errorf("logs after saving %s = %s, want the key masked", key, body)
		}
	}

	if _, status := f.call(t, "DELETE", "/api/secrets/BOX_KEY", nil); status != http.StatusOK {
		t.Fatalf("DELETE: status %d", status)
	}
	if got := f.check(t); got != "" {
		t.Errorf("Authorization after removing the key = %q, want none", got)
	}
	if got := readFile(t, f.secretsPath()); strings.Contains(got, "BOX_KEY") {
		t.Errorf("secrets.env = %q, want the line removed", got)
	}
}

func TestKeyWithLineBreakIsRefusedAndFileUnchanged(t *testing.T) {
	f := startKeysServe(t, unrelatedSecretLine)
	for name, value := range map[string]string{
		"line feed":       "sk-one-0123\nHTTPS_PROXY=http://elsewhere.example",
		"carriage return": "sk-one-0123\rHTTPS_PROXY=http://elsewhere.example",
	} {
		body, status := f.put(t, "BOX_KEY", value)
		if status != http.StatusUnprocessableEntity || !strings.Contains(body, "A key is one line.") || strings.Contains(body, "sk-one-0123") {
			t.Errorf("%s: status %d, body %s, want 422 \"A key is one line.\" without the value", name, status, body)
		}
		if got := readFile(t, f.secretsPath()); got != unrelatedSecretLine {
			t.Errorf("%s: secrets.env = %q, want it unchanged", name, got)
		}
	}
}

func TestKeyNameNoProviderUsesIsRefused(t *testing.T) {
	f := startKeysServe(t, "")
	for _, name := range []string{"HTTPS_PROXY", "OTHER_KEY"} {
		if body, status := f.put(t, name, "sk-invented-0123456789"); status != http.StatusUnprocessableEntity {
			t.Errorf("PUT %s: status %d, body %s, want 422", name, status, body)
		}
	}
	if _, err := os.Stat(f.secretsPath()); !os.IsNotExist(err) {
		t.Errorf("secrets.env exists after refused names (%v), want none", err)
	}
}
