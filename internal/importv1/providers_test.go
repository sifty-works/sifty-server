package importv1_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/config"
)

const fixtureProviders = `
stages:
  assessment: localmodel
  preparation: remotemodel
providers:
  localmodel:
    type: ollama
    model: phi3:mini
    endpoint: http://127.0.0.1:11434
    timeout_ms: 120000
    context_chars: 32768
  remotemodel:
    type: compatible
    model: gpt-4o-mini
    endpoint: https://api.openai.com/v1
    timeout_ms: 60000
    context_chars: 16384
  legacycode:
    type: claude
    model: claude-3-5-sonnet
    endpoint: ""
    timeout_ms: 90000
    context_chars: 8192
  legacyopen:
    type: opencode
    model: gpt-4o
    endpoint: ""
    timeout_ms: 90000
    context_chars: 8192
`

const fixtureRuntime = `
quick_match:
  model_passes:
    allow_paid_bulk_provider: true
paused: true
daily_limit: 25
`

func makeProviderOldDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	makeOldDB(t, dir, oldDBFile, 43)
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "providers.yaml"), []byte(fixtureProviders), 0600); err != nil {
		t.Fatalf("write providers.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "runtime.yaml"), []byte(fixtureRuntime), 0600); err != nil {
		t.Fatalf("write runtime.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("SOMEHOST_KEY=testkey\n"), 0600); err != nil {
		t.Fatalf("write secrets.env: %v", err)
	}
	return dir
}

func TestImportProviders_TypesAndCostMapped(t *testing.T) {
	fromDir := makeProviderOldDir(t)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	store, _, err := config.Open(filepath.Join(targetDir, "sifty.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()

	local, ok := cfg.Providers["localmodel"]
	if !ok {
		t.Fatal("localmodel not in providers")
	}
	if local.Type != "ollama" {
		t.Errorf("localmodel type: got %q, want ollama", local.Type)
	}
	if local.Cost != "local" {
		t.Errorf("localmodel cost: got %q, want local (loopback endpoint)", local.Cost)
	}
	if local.Model != "phi3:mini" {
		t.Errorf("localmodel model: got %q", local.Model)
	}
	if local.Context != 32768 {
		t.Errorf("localmodel context: got %d, want 32768", local.Context)
	}

	remote, ok := cfg.Providers["remotemodel"]
	if !ok {
		t.Fatal("remotemodel not in providers")
	}
	if remote.Type != "openai" {
		t.Errorf("remotemodel type: got %q, want openai (compatible→openai)", remote.Type)
	}
	if remote.Cost != "paid" {
		t.Errorf("remotemodel cost: got %q, want paid (non-loopback endpoint)", remote.Cost)
	}

	claude, ok := cfg.Providers["legacycode"]
	if !ok {
		t.Fatal("legacycode not in providers")
	}
	if claude.Type != "cli-claude" {
		t.Errorf("legacycode type: got %q, want cli-claude (claude→cli-claude)", claude.Type)
	}

	openc, ok := cfg.Providers["legacyopen"]
	if !ok {
		t.Fatal("legacyopen not in providers")
	}
	if openc.Type != "cli-opencode" {
		t.Errorf("legacyopen type: got %q, want cli-opencode (opencode→cli-opencode)", openc.Type)
	}
}

func TestImportProviders_WorkStagesMapped(t *testing.T) {
	fromDir := makeProviderOldDir(t)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	store, _, err := config.Open(filepath.Join(targetDir, "sifty.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()

	if cfg.Work["reading"] != "localmodel" {
		t.Errorf("work.reading: got %q, want localmodel (from assessment stage)", cfg.Work["reading"])
	}
	if cfg.Work["preparation"] != "remotemodel" {
		t.Errorf("work.preparation: got %q, want remotemodel", cfg.Work["preparation"])
	}
}

func TestImportProviders_RuntimeFlagsCarried(t *testing.T) {
	fromDir := makeProviderOldDir(t)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	store, _, err := config.Open(filepath.Join(targetDir, "sifty.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()

	if !cfg.AllowPaidBulk {
		t.Error("allow_paid_bulk: got false, want true")
	}
	if cfg.DailyModelLimit != 25 {
		t.Errorf("daily_model_limit: got %d, want 25", cfg.DailyModelLimit)
	}

	newDB := openNewDB(t, targetDir)
	var val string
	newDB.QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key='models_paused'`).Scan(&val)
	if val != "true" {
		t.Errorf("models_paused in settings: got %q, want true", val)
	}
}

func TestImportProviders_SecretsFileCopied(t *testing.T) {
	fromDir := makeProviderOldDir(t)
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	data, err := os.ReadFile(filepath.Join(targetDir, "secrets.env"))
	if err != nil {
		t.Fatalf("secrets.env not copied: %v", err)
	}
	if !strings.Contains(string(data), "SOMEHOST_KEY") {
		t.Errorf("secrets.env content: got %q", data)
	}
}

func TestImportProviders_ExtractionStageUsedWhenNoAssessment(t *testing.T) {
	fromDir := t.TempDir()
	makeOldDB(t, fromDir, oldDBFile, 43)
	cfgDir := filepath.Join(fromDir, "config")
	os.MkdirAll(cfgDir, 0700)
	os.WriteFile(filepath.Join(cfgDir, "providers.yaml"), []byte(`
stages:
  extraction: mymodel
providers:
  mymodel:
    type: ollama
    model: llama3
    endpoint: http://localhost:11434
`), 0600)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	store, _, err := config.Open(filepath.Join(targetDir, "sifty.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	if cfg.Work["reading"] != "mymodel" {
		t.Errorf("work.reading: got %v, want mymodel (from extraction)", cfg.Work)
	}
}
