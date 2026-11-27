package db_test

import (
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/sifty-works/sifty-server/internal/db"
)

func TestMigrationDropsOfferFactsAndRequirementCounts(t *testing.T) {
	before := fstest.MapFS{}
	names, err := fs.Glob(db.Migrations, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "028" {
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
		`INSERT INTO vacancy (id, identity, title, company, url, first_seen, last_seen)
		 VALUES (1, 'a|b', 'Engineer', 'Acme', 'https://example.com/jobs/1', '2024-01-01', '2024-01-01')`,
		`INSERT INTO vacancy (id, identity, title, company, url, first_seen, last_seen)
		 VALUES (2, 'c|d', 'Analyst', 'Globex', 'https://example.com/jobs/2', '2024-01-01', '2024-01-01')`,
		`INSERT INTO fact (vacancy, version, worker, kind, value, way, origin, worker_version) VALUES
		 (1, 1, 'offer', 'offer', '{"key":"section"}', 'vocabulary', '', 1),
		 (1, 1, 'offer', 'analysed', 'offer', 'vocabulary', '', 1),
		 (1, 1, 'requirement_lines', 'requirement', 'Go', 'vocabulary', '', 1),
		 (1, 1, 'requirement_lines', 'analysed', 'requirement_lines', 'vocabulary', '', 1)`,
		`INSERT INTO fit (profile, vacancy, version, inputs, listing, state, score, req_ok, req_no, req_maybe, req_near)
		 VALUES (1, 1, 1, 1, 1, 'scored', 70, 3, 1, 2, 1)`,
		`INSERT INTO fit (profile, vacancy, version, inputs, listing, state, score)
		 VALUES (1, 2, 1, 1, 1, 'no_facts', NULL)`,
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

	var offer, requirement int
	d.QueryRow(`SELECT count(*) FROM fact WHERE worker='offer'`).Scan(&offer)
	d.QueryRow(`SELECT count(*) FROM fact WHERE worker='requirement_lines'`).Scan(&requirement)
	if offer != 0 || requirement != 2 {
		t.Errorf("after the migration: %d offer facts and %d requirement facts, want 0 and 2", offer, requirement)
	}
	var rows, countColumns int
	d.QueryRow(`SELECT count(*) FROM fit`).Scan(&rows)
	d.QueryRow(`SELECT count(*) FROM pragma_table_info('fit') WHERE name LIKE 'req\_%' ESCAPE '\'`).Scan(&countColumns)
	if rows != 2 || countColumns != 0 {
		t.Errorf("after the migration: %d fit rows and %d count columns, want 2 and 0", rows, countColumns)
	}
	for vacancy, want := range map[int]bool{1: true, 2: false} {
		var got bool
		d.QueryRow(`SELECT COALESCE(analysed, 0) FROM fit WHERE vacancy=?`, vacancy).Scan(&got)
		if got != want {
			t.Errorf("fit row of vacancy %d: analysed %v, want %v", vacancy, got, want)
		}
	}
}
