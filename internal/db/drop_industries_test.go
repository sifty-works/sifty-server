package db_test

import (
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/sifty-works/sifty-server/internal/db"
)

func TestDroppingIndustriesKeepsVacanciesAndDecisions(t *testing.T) {
	before := fstest.MapFS{}
	names, err := fs.Glob(db.Migrations, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "022" {
			break
		}
		data, err := fs.ReadFile(db.Migrations, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		before[name] = &fstest.MapFile{Data: data}
	}

	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path, before)
	if err != nil {
		t.Fatalf("open before: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO vacancy (id, identity, title, company, url, first_seen, last_seen, industries)
		 VALUES (1, 'a|b', 'Engineer', 'Acme', 'https://example.com/jobs/1', '2024-01-01', '2024-01-01', '["Software","Web3"]')`,
		`INSERT INTO vacancy (id, identity, title, company, url, first_seen, last_seen)
		 VALUES (2, 'c|d', 'Analyst', 'Globex', 'https://example.com/jobs/2', '2024-01-01', '2024-01-01')`,
		`INSERT INTO decision (profile, vacancy, stage, decided_at) VALUES (1, 1, 'shortlist', '2024-01-02')`,
		`INSERT INTO decision (profile, vacancy, stage, decided_at) VALUES (1, 2, 'dismissed', '2024-01-02')`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	d.Close()

	d, err = db.Open(path, db.Migrations)
	if err != nil {
		t.Fatalf("open after: %v", err)
	}
	defer d.Close()

	var vacancies, decisions, hasColumn int
	d.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&vacancies)
	d.QueryRow(`SELECT count(*) FROM decision`).Scan(&decisions)
	d.QueryRow(`SELECT count(*) FROM pragma_table_info('vacancy') WHERE name='industries'`).Scan(&hasColumn)
	if vacancies != 2 || decisions != 2 {
		t.Errorf("after the migration: %d vacancies and %d decisions, want 2 and 2", vacancies, decisions)
	}
	if hasColumn != 0 {
		t.Errorf("vacancy still has the industries column")
	}
}
