package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/db"
)

type backupFixture struct {
	dir  string
	db   *db.DB
	open func()
}

func newBackupFixture(t *testing.T) backupFixture {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	for _, word := range []string{"alpha", "beta", "gamma"} {
		if _, err := d.Exec(`INSERT INTO exclusion (kind, value, norm, created) VALUES ('word', ?, ?, '2026-01-01T00:00:00Z')`, word, word); err != nil {
			t.Fatalf("seed exclusion: %v", err)
		}
	}
	files := map[string]string{
		"sifty.yaml":                 "port: 7373\n",
		"secrets.env":                "EXAMPLE_API_KEY=not-a-real-key\n",
		"files/note.txt":             "invented note",
		"files/nested/me_photo":      "invented photo bytes",
		"backups/sifty-old-v1.db":    "old copy",
		"logs/sifty.log":             "log line\n",
		"sifty.lock":                 "",
		"vocab/sites.yaml":           "sites: {}\n",
		"sifty.yaml.broken-20260101": "broken",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return backupFixture{dir: dir, db: d}
}

func readArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = data
	}
}

func unpackArchive(t *testing.T, archive map[string][]byte, dst string) {
	t.Helper()
	for name, data := range archive {
		if strings.HasSuffix(name, "/") {
			continue
		}
		path := filepath.Join(dst, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupHoldsCommittedRowsAndOpensWithoutMigrating(t *testing.T) {
	fx := newBackupFixture(t)
	tx, err := fx.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO exclusion (kind, value, norm, created) VALUES ('word', 'pending', 'pending', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "backup.tar.gz")
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"backup", "--data-dir", fx.dir, "--out", out}, io.Discard, &stderr); err != nil {
		t.Fatalf("backup: %v; stderr %q", err, stderr.String())
	}

	archive := readArchive(t, out)
	var names []string
	for name := range archive {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"files/", "files/nested/", "files/nested/me_photo", "files/note.txt", "sifty.db", "sifty.yaml", "vocab/sites.yaml"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}

	copyDir := t.TempDir()
	unpackArchive(t, archive, copyDir)
	restored, err := db.Open(filepath.Join(copyDir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open archived db: %v", err)
	}
	defer restored.Close()
	var n int
	if err := restored.QueryRow(`SELECT count(*) FROM exclusion`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("archived db holds %d exclusions, want the 3 committed", n)
	}
	if restored.Version != fx.db.Version {
		t.Errorf("restored version = %d, want %d", restored.Version, fx.db.Version)
	}
	if _, err := os.Stat(filepath.Join(copyDir, "backups")); err == nil {
		t.Errorf("opening the restored folder made a migration copy, so a migration was pending")
	}
}

func TestBackupRefusesExistingArchive(t *testing.T) {
	fx := newBackupFixture(t)
	existing := filepath.Join(t.TempDir(), "taken.tar.gz")
	if err := os.WriteFile(existing, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"backup", "--data-dir", fx.dir, "--out", existing}, io.Discard, &stderr)
	if err == nil || !strings.Contains(err.Error(), existing) || !strings.Contains(stderr.String(), existing) {
		t.Errorf("existing out: err %v, stderr %q; want a refusal naming %s", err, stderr.String(), existing)
	}
	if got, _ := os.ReadFile(existing); string(got) != "keep me" {
		t.Errorf("existing file was changed: %q", got)
	}
}

func TestBackupDoesNotMigrateTheLiveDatabase(t *testing.T) {
	fx := newBackupFixture(t)
	lower := fx.db.Version - 1
	if _, err := fx.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", lower)); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := run(context.Background(), []string{"backup", "--data-dir", fx.dir, "--out", out}, io.Discard, io.Discard); err != nil {
		t.Fatalf("backup: %v", err)
	}

	var live int
	fx.db.QueryRow(`PRAGMA user_version`).Scan(&live)
	if live != lower {
		t.Errorf("live user_version = %d, want %d", live, lower)
	}
	entries, err := os.ReadDir(filepath.Join(fx.dir, "backups"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "sifty-old-v1.db" {
		t.Errorf("backups folder after the command: %v, %v", entries, err)
	}

	copyDir := t.TempDir()
	unpackArchive(t, readArchive(t, out), copyDir)
	raw, err := sql.Open("sqlite", filepath.Join(copyDir, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var archived int
	if err := raw.QueryRow(`PRAGMA user_version`).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived != lower {
		t.Errorf("archived user_version = %d, want %d", archived, lower)
	}
}
