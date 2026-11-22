package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/config"
)

func openStore(t *testing.T) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, _, err := config.Open(filepath.Join(dir, "sifty.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func openStoreWith(t *testing.T, content string) (*config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sifty.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	s, _, err := config.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestOpenBrokenFileWithValidBackupUsesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sifty.yaml")
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(backupDir, "sifty-20240101T120000.000000000Z.yaml")
	if err := os.WriteFile(backup, []byte("port: 9191\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{{{: not valid yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, result, err := config.Open(path)
	if err != nil {
		t.Fatalf("Open should recover from broken file: %v", err)
	}
	if result.Broken == "" {
		t.Fatal("want a recovery with a renamed broken file")
	}
	if s.Get().Port != 9191 {
		t.Errorf("port should come from backup: got %d", s.Get().Port)
	}
}

func TestOpenBrokenFileNoBackupUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sifty.yaml")
	if err := os.WriteFile(path, []byte("{{{: not valid yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, result, err := config.Open(path)
	if err != nil {
		t.Fatalf("Open should recover without backup: %v", err)
	}
	if result.Broken == "" {
		t.Fatal("want a recovery with a renamed broken file")
	}
	if s.Get().Port != config.Defaults().Port {
		t.Errorf("port should be default: got %d", s.Get().Port)
	}
}

func TestOpenValidFileIsNeverRenamed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sifty.yaml")
	if err := os.WriteFile(path, []byte("port: 8181\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, result, err := config.Open(path)
	if err != nil {
		t.Fatalf("Open valid file should not error: %v", err)
	}
	if result.Broken != "" || result.Err != "" {
		t.Errorf("valid file should produce no broken recovery, got %+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("valid file should still exist: %v", err)
	}
}

func TestOpenUnreadableFileIsNotRenamed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sifty.yaml")
	if err := os.WriteFile(path, []byte("port: 7373\n"), 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0600)
	_, _, err := config.Open(path)
	if err == nil {
		t.Fatal("unreadable file should return error")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".broken") {
			t.Errorf("unreadable file should not be renamed; found %s", e.Name())
		}
	}
}

func TestOpenSourceSurvivesBrokenFile(t *testing.T) {
	s, dir := openStoreWith(t, "port: 7373\n")
	s.Update(func(c *config.Config) error {
		c.Sources = append(c.Sources, config.SourceConfig{URL: "https://example.com/jobs", Enabled: true})
		return nil
	})
	path := filepath.Join(dir, "sifty.yaml")
	if err := os.WriteFile(path, []byte("{{{: broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s2, result, err := config.Open(path)
	if err != nil {
		t.Fatalf("Open should recover: %v", err)
	}
	if result.Broken == "" {
		t.Fatal("want at least one recovery with a renamed broken file")
	}
	srcs := s2.Get().Sources
	if len(srcs) == 0 {
		t.Error("sources from backup should be restored")
	} else if srcs[0].URL != "https://example.com/jobs" {
		t.Errorf("source URL = %q", srcs[0].URL)
	}
}

func TestOpenBrokenFileIsRestoredForNextStart(t *testing.T) {
	s, dir := openStoreWith(t, "port: 7373\n")
	s.Update(func(c *config.Config) error {
		c.Sources = append(c.Sources, config.SourceConfig{URL: "https://example.com/jobs", Enabled: true})
		return nil
	})
	path := filepath.Join(dir, "sifty.yaml")
	if err := os.WriteFile(path, []byte("{{{: broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.Open(path); err != nil {
		t.Fatalf("first Open should recover: %v", err)
	}
	s2, result, err := config.Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if result.Broken != "" || len(result.Unknown) > 0 {
		t.Errorf("second open should report nothing recovered, got %+v", result)
	}
	srcs := s2.Get().Sources
	if len(srcs) == 0 {
		t.Error("second open should have backup sources restored")
	} else if srcs[0].URL != "https://example.com/jobs" {
		t.Errorf("source URL = %q", srcs[0].URL)
	}
}

func TestUpdateChangesInMemoryConfig(t *testing.T) {
	s, _ := openStore(t)
	if err := s.Update(func(c *config.Config) error { c.Port = 9999; return nil }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if s.Get().Port != 9999 {
		t.Errorf("port after Update: got %d, want 9999", s.Get().Port)
	}
}

func TestUpdateWritesFileWithChangedKey(t *testing.T) {
	s, dir := openStore(t)
	s.Update(func(c *config.Config) error { c.Port = 9000; return nil })
	data, _ := os.ReadFile(filepath.Join(dir, "sifty.yaml"))
	if !strings.Contains(string(data), "port: 9000") {
		t.Errorf("file should contain 'port: 9000'; got:\n%s", data)
	}
}

func TestUpdateKeepsComments(t *testing.T) {
	s, dir := openStoreWith(t, "# my settings\nport: 7373\ndebug: false\n")
	path := filepath.Join(dir, "sifty.yaml")
	if err := s.Update(func(c *config.Config) error {
		c.Debug = true
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# my settings") {
		t.Errorf("comment should be preserved; got:\n%s", data)
	}
	if !strings.Contains(string(data), "debug: true") {
		t.Errorf("debug should be true; got:\n%s", data)
	}
	if !strings.Contains(string(data), "port: 7373") {
		t.Errorf("port should still be present; got:\n%s", data)
	}
}

func TestUpdateValidationFailureWritesNothing(t *testing.T) {
	s, dir := openStore(t)

	err := s.Update(func(c *config.Config) error {
		c.Port = 99999
		return nil
	})
	if err == nil {
		t.Fatal("invalid update should return error")
	}
	var inv apperr.Invalid
	if !errors.As(err, &inv) {
		t.Errorf("validation error should be apperr.Invalid, got %T: %v", err, err)
	}

	if _, err2 := os.Stat(filepath.Join(dir, "sifty.yaml")); err2 == nil {
		t.Error("file should not have been written after failed validation")
	}
	if s.Get().Port == 99999 {
		t.Error("in-memory config should not have changed after failed validation")
	}
}

func TestUpdateFnErrorIsInvalid(t *testing.T) {
	s, _ := openStore(t)
	err := s.Update(func(c *config.Config) error {
		return errors.New("domain rule violation")
	})
	if err == nil {
		t.Fatal("fn error should propagate")
	}
	var inv apperr.Invalid
	if !errors.As(err, &inv) {
		t.Errorf("fn error should be apperr.Invalid, got %T", err)
	}
}

func TestUpdateWritesBackup(t *testing.T) {
	s, dir := openStoreWith(t, "port: 7373\n")

	s.Update(func(c *config.Config) error { c.Port = 8000; return nil })

	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("backups dir should exist: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("want 1 backup file, got %d", len(entries))
	}
}

func TestUpdateNoBackupWhenFileDidNotExist(t *testing.T) {
	s, dir := openStore(t)
	s.Update(func(c *config.Config) error { c.Port = 8000; return nil })

	if _, err := os.Stat(filepath.Join(dir, "backups")); err == nil {
		t.Error("backups dir should not exist when file did not exist before update")
	}
}

func TestUpdateKeepsLastTenBackups(t *testing.T) {
	s, dir := openStoreWith(t, "port: 7373\n")

	for i := 8000; i < 8012; i++ {
		port := i
		s.Update(func(c *config.Config) error {
			c.Port = port
			return nil
		})
		time.Sleep(time.Millisecond)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("backups dir: %v", err)
	}
	if len(entries) > 10 {
		t.Errorf("want at most 10 backups, got %d", len(entries))
	}
}

func TestUpdateAddsSource(t *testing.T) {
	s, dir := openStore(t)
	s.Update(func(c *config.Config) error {
		c.Sources = append(c.Sources, config.SourceConfig{
			URL: "https://example.com/jobs", Enabled: true,
		})
		return nil
	})

	data, _ := os.ReadFile(filepath.Join(dir, "sifty.yaml"))
	if !strings.Contains(string(data), "https://example.com/jobs") {
		t.Errorf("file should contain the new source URL; got:\n%s", data)
	}
}

func TestUpdateRemovesSource(t *testing.T) {
	s, dir := openStoreWith(t, "sources:\n  - url: https://example.com/jobs1\n  - url: https://example.com/jobs2\n")
	path := filepath.Join(dir, "sifty.yaml")

	s.Update(func(c *config.Config) error {
		c.Sources = c.Sources[:1]
		return nil
	})

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "jobs2") {
		t.Errorf("removed source should not appear in file; got:\n%s", data)
	}
	if !strings.Contains(string(data), "jobs1") {
		t.Errorf("remaining source should still be in file; got:\n%s", data)
	}
}

func TestUpdateChangesSourceKeepsComment(t *testing.T) {
	s, dir := openStoreWith(t, "sources:\n  # job board\n  - url: https://example.com/jobs\n    enabled: true\n")
	path := filepath.Join(dir, "sifty.yaml")

	s.Update(func(c *config.Config) error {
		c.Sources[0].Enabled = false
		return nil
	})

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# job board") {
		t.Errorf("source comment should be preserved; got:\n%s", data)
	}
	if !strings.Contains(string(data), "enabled: false") {
		t.Errorf("enabled should be false; got:\n%s", data)
	}
}

func TestUpdateFailsWhenFileIsUnparseable(t *testing.T) {
	s, dir := openStoreWith(t, "port: 7373\n")
	path := filepath.Join(dir, "sifty.yaml")

	garbage := []byte("{{{{: not valid yaml\n")
	os.WriteFile(path, garbage, 0600)

	err := s.Update(func(c *config.Config) error { c.Port = 8080; return nil })
	if err == nil {
		t.Fatal("Update should fail when the file on disk is unparseable")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(garbage) {
		t.Errorf("file should be unchanged after failed Update; got:\n%s", got)
	}
	if s.Get().Port != 7373 {
		t.Errorf("in-memory port should still be 7373, got %d", s.Get().Port)
	}
}

func TestUpdateWritesIntervalInShortForm(t *testing.T) {
	s, dir := openStore(t)
	s.Update(func(c *config.Config) error {
		c.Collection.Interval = config.Duration(90 * time.Minute)
		return nil
	})
	data, _ := os.ReadFile(filepath.Join(dir, "sifty.yaml"))
	if !strings.Contains(string(data), "interval: 1h30m") {
		t.Errorf("90m should serialize as 1h30m; got:\n%s", data)
	}
}

func TestUpdateWritesOnlyChangedWeights(t *testing.T) {
	s, dir := openStore(t)
	if err := s.Update(func(c *config.Config) error { c.Weights["money"] = 0.3; return nil }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "sifty.yaml"))
	if string(data) != "weights:\n  money: 0.3\n" {
		t.Errorf("file = %q", data)
	}
	if config.Defaults().Weights["money"] != 0.20 {
		t.Error("an update changed the defaults")
	}
}

func TestWorkProvidersKeepTheirShapeInTheFileWhenSaved(t *testing.T) {
	s, dir := openStoreWith(t, "providers:\n  a: {type: ollama}\n  b: {type: ollama}\nwork:\n  preparation: a\n  dreaming: a\n  reading: b\n")
	before := s.Get()
	if got := before.WorkProviderFor("preparation"); got != "a" {
		t.Errorf("preparation before saving: %q", got)
	}
	err := s.Update(func(c *config.Config) error {
		for _, kind := range []string{"reading", "shortlist_check", "preparation"} {
			if !c.SetWorkProvider(kind, "b") {
				t.Errorf("kind %s refused", kind)
			}
		}
		if c.SetWorkProvider("dreaming", "b") {
			t.Error("an unknown kind was accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "sifty.yaml"))
	if want := "work:\n  reading: b\n  shortlist_check: b\n  preparation: b\n"; !strings.Contains(string(data), want) {
		t.Errorf("work block after saving:\n%s", data)
	}
	reopened, _, err := config.Open(filepath.Join(dir, "sifty.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	after := reopened.Get()
	for _, kind := range config.WorkKinds {
		if got := after.WorkProviderFor(kind); got != "b" {
			t.Errorf("%s after reopening: %q", kind, got)
		}
	}
}
