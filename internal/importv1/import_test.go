package importv1_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/importv1"
	"github.com/sifty-works/sifty-server/internal/listing"
	_ "modernc.org/sqlite"
)

const oldSchema = `
CREATE TABLE jobs(
  id INTEGER PRIMARY KEY,
  identity TEXT NOT NULL,
  title TEXT NOT NULL,
  company TEXT NOT NULL,
  original_url TEXT NOT NULL,
  apply_url TEXT,
  description TEXT NOT NULL,
  description_status TEXT NOT NULL,
  location TEXT,
  published_at TEXT,
  expires_at TEXT,
  status TEXT NOT NULL DEFAULT 'new',
  first_seen TEXT NOT NULL,
  last_seen TEXT NOT NULL,
  assessment TEXT,
  assessment_error TEXT,
  apply_status TEXT NOT NULL DEFAULT 'not resolved',
  tags TEXT NOT NULL DEFAULT '[]',
  seen_at TEXT,
  applied_at TEXT,
  match_score INTEGER NOT NULL DEFAULT 0,
  match_profile_revision INTEGER NOT NULL DEFAULT 0,
  outcome_reason TEXT,
  published_basis TEXT,
  description_html TEXT,
  needs_refetch INTEGER NOT NULL DEFAULT 0,
  employer_found_at TEXT
);
CREATE TABLE job_aliases(old_id INTEGER PRIMARY KEY, new_id INTEGER NOT NULL);
CREATE TABLE deleted_listings(hash TEXT PRIMARY KEY);
CREATE TABLE sightings(
  job_id INTEGER NOT NULL REFERENCES jobs(id),
  source TEXT NOT NULL,
  url TEXT NOT NULL UNIQUE,
  seen TEXT NOT NULL
);
CREATE TABLE id_sequence(name TEXT PRIMARY KEY, value INTEGER NOT NULL);
CREATE TABLE collection_runs(
  id INTEGER PRIMARY KEY,
  started TEXT NOT NULL,
  finished TEXT,
  status TEXT NOT NULL,
  pid INTEGER NOT NULL,
  mode TEXT NOT NULL,
  config_hash TEXT NOT NULL
);
CREATE TABLE stop_words(
  id         INTEGER PRIMARY KEY,
  word       TEXT    NOT NULL,
  created_at TEXT    NOT NULL,
  UNIQUE(word)
);
CREATE TABLE block_rules(
  id         INTEGER PRIMARY KEY,
  kind       TEXT    NOT NULL,
  value      TEXT    NOT NULL,
  created_at TEXT    NOT NULL,
  UNIQUE(kind, value)
);
CREATE TABLE user_profiles(
  id          TEXT    PRIMARY KEY,
  revision    INTEGER NOT NULL,
  name        TEXT    NOT NULL DEFAULT '',
  data        TEXT    NOT NULL,
  created_at  TEXT    NOT NULL,
  updated_at  TEXT    NOT NULL,
  archived_at TEXT,
  headline    TEXT    NOT NULL DEFAULT ''
);
CREATE TABLE profile_job_stages(
  id             INTEGER PRIMARY KEY,
  profile_id     TEXT    NOT NULL REFERENCES user_profiles(id),
  job_id         INTEGER NOT NULL REFERENCES jobs(id),
  status         TEXT    NOT NULL,
  outcome_reason TEXT,
  updated_at     TEXT    NOT NULL,
  UNIQUE(profile_id, job_id)
);
CREATE TABLE profile_workflow_history(
  id         INTEGER PRIMARY KEY,
  profile_id TEXT    NOT NULL REFERENCES user_profiles(id),
  job_id     INTEGER NOT NULL REFERENCES jobs(id),
  created    TEXT    NOT NULL,
  from_stage TEXT    NOT NULL,
  to_stage   TEXT    NOT NULL
);
CREATE TABLE users(
  id         TEXT    PRIMARY KEY,
  revision   INTEGER NOT NULL,
  data       TEXT    NOT NULL,
  created_at TEXT    NOT NULL,
  updated_at TEXT    NOT NULL
);
CREATE TABLE profile_qa(
  id         TEXT NOT NULL PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id),
  question   TEXT NOT NULL,
  answer     TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE app_settings(
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

func makeOldDB(t *testing.T, dir, name string, v int) *sql.DB {
	t.Helper()
	path := filepath.Join(dir, name)
	oldDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	t.Cleanup(func() { oldDB.Close() })
	if _, err := oldDB.Exec(oldSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := oldDB.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	return oldDB
}

func makeOldDir(t *testing.T, yamlContent string) string {
	t.Helper()
	dir := t.TempDir()
	oldDB := makeOldDB(t, dir, oldDBFile, 43)
	_ = oldDB

	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if yamlContent != "" {
		if err := os.WriteFile(filepath.Join(cfgDir, "search.yaml"), []byte(yamlContent), 0600); err != nil {
			t.Fatalf("write search.yaml: %v", err)
		}
	}
	return dir
}

func openOldDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	path := filepath.Join(dir, oldDBFile)
	oldDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open old db for writing: %v", err)
	}
	t.Cleanup(func() { oldDB.Close() })
	return oldDB
}

const oldDBFile = "store.sqlite"

func runImport(t *testing.T, fromDir, targetDir string) string {
	t.Helper()
	dbPath := filepath.Join(targetDir, "sifty.db")
	settingsPath := filepath.Join(targetDir, "sifty.yaml")
	var buf bytes.Buffer
	if err := importv1.Import(context.Background(), fromDir, dbPath, settingsPath, &buf); err != nil {
		t.Fatalf("import: %v", err)
	}
	return buf.String()
}

func openNewDB(t *testing.T, targetDir string) *db.DB {
	t.Helper()
	newDB, err := db.Open(filepath.Join(targetDir, "sifty.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open new db: %v", err)
	}
	t.Cleanup(func() { newDB.Close() })
	return newDB
}

func TestImport_AllFieldsCarriedOver(t *testing.T) {
	fromDir := makeOldDir(t, `
version: 1
sites:
  - name: Acme Board
    url: https://boards.greenhouse.io/acmeco
    enabled: true
search: {}
preferences: {}
`)
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url, apply_url,
		  description, description_status, location, published_at, published_basis,
		  expires_at, first_seen, last_seen, tags, description_html)
		VALUES (1, 'acme|engineer', 'Senior Engineer', 'Acme Corp',
		  'https://boards.greenhouse.io/acmeco/jobs/42',
		  'https://boards.greenhouse.io/acmeco/jobs/42',
		  '', 'available',
		  'Remote', '2024-01-10T09:00:00Z', 'datePosted',
		  '2024-06-01T00:00:00Z',
		  '2024-01-10T09:00:00Z', '2024-01-15T12:00:00Z',
		  '["backend","go"]',
		  '<p>Full job description.</p>')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	_, err = oldDB.Exec(`
		INSERT INTO sightings (job_id, source, url, seen)
		VALUES (1, 'Acme Board', 'https://boards.greenhouse.io/acmeco/jobs/42', '2024-01-10T09:00:00Z')`)
	if err != nil {
		t.Fatalf("insert sighting: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var id int64
	var title, company, url, applyURL, location, tags, identity string
	var publishedAt, publishedBasis, expiresAt, firstSeen, lastSeen string
	var textState, contentHash string
	var version int
	err = newDB.QueryRow(`
		SELECT id, identity, title, company, url, apply_url, location, tags,
		       published_at, published_basis, expires_at,
		       first_seen, last_seen, text_state, content_hash, version
		FROM vacancy WHERE id=1`).Scan(
		&id, &identity, &title, &company, &url, &applyURL, &location, &tags,
		&publishedAt, &publishedBasis, &expiresAt,
		&firstSeen, &lastSeen, &textState, &contentHash, &version)
	if err != nil {
		t.Fatalf("query vacancy: %v", err)
	}

	if title != "Senior Engineer" {
		t.Errorf("title: got %q, want %q", title, "Senior Engineer")
	}
	if company != "Acme Corp" {
		t.Errorf("company: got %q, want %q", company, "Acme Corp")
	}
	if !strings.HasPrefix(url, "https://boards.greenhouse.io") {
		t.Errorf("url: got %q", url)
	}
	if applyURL == "" {
		t.Errorf("apply_url: empty, want concrete ATS address")
	}
	if location != "Remote" {
		t.Errorf("location: got %q", location)
	}
	if tags != `["backend","go"]` {
		t.Errorf("tags: got %q", tags)
	}
	if publishedAt != "2024-01-10T09:00:00Z" {
		t.Errorf("published_at: got %q", publishedAt)
	}
	if publishedBasis != "datePosted" {
		t.Errorf("published_basis: got %q", publishedBasis)
	}
	if expiresAt != "2024-06-01T00:00:00Z" {
		t.Errorf("expires_at: got %q", expiresAt)
	}
	if firstSeen != "2024-01-10T09:00:00Z" {
		t.Errorf("first_seen: got %q", firstSeen)
	}
	if lastSeen != "2024-01-15T12:00:00Z" {
		t.Errorf("last_seen: got %q", lastSeen)
	}
	if textState != "full" {
		t.Errorf("text_state: got %q, want full", textState)
	}
	if contentHash == "" {
		t.Errorf("content_hash: empty")
	}
	if version != 1 {
		t.Errorf("version: got %d, want 1", version)
	}
	wantIdentity := listing.Identity("Acme Corp", "Senior Engineer")
	if identity != wantIdentity {
		t.Errorf("identity: got %q, want %q", identity, wantIdentity)
	}
}

func TestImport_TextStates(t *testing.T) {
	cases := []struct {
		status    string
		desc      string
		descHTML  string
		wantState string
	}{
		{"blocked_by_site", "", "", "blocked"},
		{"blocked_something", "some text", "", "blocked"},
		{"available", "<p>full text</p>", "", "full"},
		{"available_full", "", "<p>html</p>", "full"},
		{"other_status", "some text", "", "summary"},
		{"", "", "", "not_fetched"},
		{"pending", "", "", "not_fetched"},
	}

	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	for i, tc := range cases {
		descHTML := "NULL"
		descHTMLVal := interface{}(nil)
		if tc.descHTML != "" {
			descHTMLVal = tc.descHTML
		}
		_, err := oldDB.Exec(`
			INSERT INTO jobs (id, identity, title, company, original_url,
			  description, description_status, first_seen, last_seen, description_html)
			VALUES (?, 'co|title', 'Title', 'Co',
			  ?,
			  ?, ?, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', ?)`,
			i+1,
			fmt.Sprintf("https://example.com/jobs/%d", i+1),
			tc.desc, tc.status, descHTMLVal,
		)
		_ = descHTML
		if err != nil {
			t.Fatalf("insert job %d: %v", i+1, err)
		}
	}
	insertProfile(t, oldDB, "px", "Tester", "2024-01-01T00:00:00Z", false)
	for i := range cases {
		insertStage(t, oldDB, "px", int64(i+1), "rejected", "", "2024-01-01T00:00:00Z")
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	for i, tc := range cases {
		var state string
		err := newDB.QueryRow(`SELECT text_state FROM vacancy WHERE id=?`, i+1).Scan(&state)
		if err != nil {
			t.Errorf("case %d (%q): query: %v", i, tc.status, err)
			continue
		}
		if state != tc.wantState {
			t.Errorf("case %d status=%q: got state %q, want %q", i, tc.status, state, tc.wantState)
		}
	}
}

func TestImport_PlainTextDescription(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	plainText := "Requirements\n- Go experience\n- SQL knowledge"
	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (1, 'co|t', 'Title', 'Co', 'https://example.com/jobs/1',
		  ?, 'available', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`,
		plainText)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var body []byte
	var hash string
	err = newDB.QueryRow(`SELECT body, content_hash FROM vacancy WHERE id=1`).Scan(&body, &hash)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	normalized := listing.Normalize(importv1.PlainToHTML(plainText))
	wantBody, wantHash, err := catalog.Encode(normalized)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if hash != wantHash {
		t.Errorf("hash: got %q, want %q", hash, wantHash)
	}
	if !bytes.Equal(body, wantBody) {
		t.Errorf("body: mismatch")
	}
}

func TestImport_EncodeConsistencyWithUpsert(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	htmlText := "<p>We are hiring a Go engineer to work on <strong>interesting</strong> systems.</p>"
	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen, description_html)
		VALUES (1, 'alpha|engineer', 'Engineer', 'Alpha',
		  'https://example.com/jobs/1',
		  '', 'available', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', ?)`,
		htmlText)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	_, err = oldDB.Exec(`
		INSERT INTO sightings (job_id, source, url, seen)
		VALUES (1, 'alpha', 'https://example.com/jobs/1', '2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert sighting: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	cat := catalog.New(newDB, time.Now)
	p := listing.Posting{
		URL:       "https://example.com/jobs/1",
		Title:     "Engineer",
		Company:   "Alpha",
		Text:      htmlText,
		TextState: listing.TextFull,
	}
	_, outcome, err := cat.Upsert(context.Background(), p, "alpha")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if outcome != catalog.OutcomeUnchanged {
		t.Errorf("upsert outcome: got %v, want Unchanged", outcome)
	}
}

func TestImport_PublicationRules(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, published_at, published_basis,
		  first_seen, last_seen)
		VALUES
		  (1,'co|a','A','Co','https://example.com/jobs/1','t','available','2024-03-15T00:00:00Z','datePosted','2024-03-15T00:00:00Z','2024-03-15T00:00:00Z'),
		  (2,'co|b','B','Co','https://example.com/jobs/2','t','available','2024-03-15T00:00:00Z','pubDate','2024-03-15T00:00:00Z','2024-03-15T00:00:00Z'),
		  (3,'co|c','C','Co','https://example.com/jobs/3','t','available','2024-03-15T00:00:00Z','updatedAt','2024-03-15T00:00:00Z','2024-03-15T00:00:00Z'),
		  (4,'co|d','D','Co','https://example.com/jobs/4','t','available',?,'datePosted','2024-03-15T00:00:00Z','2024-03-15T00:00:00Z'),
		  (5,'co|e','E','Co','https://example.com/jobs/5','t','available','not-a-date','datePosted','2024-03-15T00:00:00Z','2024-03-15T00:00:00Z'),
		  (6,'co|f','F','Co','https://example.com/jobs/6','t','available','2024-03-15T00:00:00Z',NULL,'2024-03-15T00:00:00Z','2024-03-15T00:00:00Z')`,
		future)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	type row struct {
		publishedAt    string
		publishedBasis string
	}
	read := func(id int) row {
		var r row
		newDB.QueryRow(`SELECT published_at, published_basis FROM vacancy WHERE id=?`, id).Scan(&r.publishedAt, &r.publishedBasis)
		return r
	}

	r1 := read(1)
	if r1.publishedAt != "2024-03-15T00:00:00Z" || r1.publishedBasis != "datePosted" {
		t.Errorf("id=1: got published_at=%q basis=%q", r1.publishedAt, r1.publishedBasis)
	}
	r2 := read(2)
	if r2.publishedAt != "2024-03-15T00:00:00Z" || r2.publishedBasis != "pubDate" {
		t.Errorf("id=2: got published_at=%q basis=%q", r2.publishedAt, r2.publishedBasis)
	}
	r3 := read(3)
	if r3.publishedAt != "2024-03-15T00:00:00Z" || r3.publishedBasis != "" {
		t.Errorf("id=3: got published_at=%q basis=%q", r3.publishedAt, r3.publishedBasis)
	}
	r4 := read(4)
	if r4.publishedAt != "" {
		t.Errorf("id=4: future date not dropped; got %q", r4.publishedAt)
	}
	r5 := read(5)
	if r5.publishedAt != "" {
		t.Errorf("id=5: bad date not dropped; got %q", r5.publishedAt)
	}
	r6 := read(6)
	if r6.publishedAt != "2024-03-15T00:00:00Z" || r6.publishedBasis != "" {
		t.Errorf("id=6: got published_at=%q basis=%q", r6.publishedAt, r6.publishedBasis)
	}
}

func TestImport_VacancyWithoutEmployerIsLeftOutWithItsDecisionAndCounted(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)
	insertProfile(t, oldDB, "pa", "Alice", "2024-01-01T00:00:00Z", false)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (1, 'unknown|engineer', 'Engineer', 'Unknown', 'https://example.com/jobs/1',
		  't', 'available', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z'),
		  (2, 'northwind|engineer', 'Engineer', 'Northwind', 'https://example.com/jobs/2',
		  't', 'available', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	insertStage(t, oldDB, "pa", 1, "saved", "", "2024-06-02T10:00:00Z")
	insertStage(t, oldDB, "pa", 2, "saved", "", "2024-06-02T10:00:00Z")

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var vacancies, decisions int
	newDB.QueryRow(`SELECT count(*) FROM vacancy WHERE id=1`).Scan(&vacancies)
	newDB.QueryRow(`SELECT count(*) FROM decision WHERE vacancy=1`).Scan(&decisions)
	if vacancies != 0 || decisions != 0 {
		t.Errorf("vacancy without employer: got %d vacancies and %d decisions, want none", vacancies, decisions)
	}
	newDB.QueryRow(`SELECT count(*) FROM vacancy WHERE id=2`).Scan(&vacancies)
	newDB.QueryRow(`SELECT count(*) FROM decision WHERE vacancy=2`).Scan(&decisions)
	if vacancies != 1 || decisions != 1 {
		t.Errorf("vacancy with employer: got %d vacancies and %d decisions, want 1 and 1", vacancies, decisions)
	}
	if !strings.Contains(report, "dropped (no employer found): 1") {
		t.Errorf("report does not count the vacancy left out: %s", report)
	}
}

func TestImport_ApplyURLKeptOnlyConcrete(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url, apply_url,
		  description, description_status, first_seen, last_seen)
		VALUES
		  (1,'co|a','A','Co','https://example.com/jobs/1',
		   'https://boards.greenhouse.io/acme/jobs/123',
		   't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),
		  (2,'co|b','B','Co','https://example.com/jobs/2',
		   'https://boards.greenhouse.io/acme',
		   't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var apply1, apply2 string
	newDB.QueryRow(`SELECT apply_url FROM vacancy WHERE id=1`).Scan(&apply1)
	newDB.QueryRow(`SELECT apply_url FROM vacancy WHERE id=2`).Scan(&apply2)
	if apply1 == "" {
		t.Errorf("id=1 apply_url: empty, want concrete ATS")
	}
	if apply2 != "" {
		t.Errorf("id=2 apply_url: got %q, want empty (board root)", apply2)
	}
}

func TestImport_DuplicateCanonicalAddress(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES
		  (1,'co|eng','Engineer','Co','https://example.com/jobs/10?utm_source=gh',
		   '<p>full text</p>','available','2024-01-01T00:00:00Z','2024-01-05T00:00:00Z'),
		  (2,'co|eng','Engineer','Co','https://example.com/jobs/10?utm_medium=x',
		   't','available','2024-01-02T00:00:00Z','2024-01-10T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var n int
	newDB.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&n)
	if n != 1 {
		t.Errorf("vacancy count: got %d, want 1", n)
	}

	var keptID, aliasedID int64
	newDB.QueryRow(`SELECT id FROM vacancy`).Scan(&keptID)
	if keptID != 1 {
		t.Errorf("kept id: got %d, want 1", keptID)
	}
	newDB.QueryRow(`SELECT old_id FROM vacancy_alias WHERE new_id=1`).Scan(&aliasedID)
	if aliasedID != 2 {
		t.Errorf("aliased id: got %d, want 2", aliasedID)
	}

	if !strings.Contains(report, "kept=1 aliased=2") {
		t.Errorf("report missing duplicate pair: %s", report)
	}

	var lastSeen string
	newDB.QueryRow(`SELECT last_seen FROM vacancy WHERE id=1`).Scan(&lastSeen)
	if lastSeen != "2024-01-10T00:00:00Z" {
		t.Errorf("last_seen: got %q, want 2024-01-10T00:00:00Z", lastSeen)
	}
}

func TestImport_SightingSourceMapping(t *testing.T) {
	fromDir := makeOldDir(t, `
version: 1
sites:
  - name: Greenhouse Acme
    url: https://boards.greenhouse.io/acmeco
    enabled: true
`)
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES
		  (1,'acme|eng','Eng','Acme','https://boards.greenhouse.io/acmeco/jobs/1',
		   't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),
		  (2,'other|eng','Eng','Other','https://example.org/jobs/2',
		   't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert jobs: %v", err)
	}
	_, err = oldDB.Exec(`
		INSERT INTO sightings (job_id, source, url, seen)
		VALUES
		  (1, 'Greenhouse Acme', 'https://boards.greenhouse.io/acmeco/jobs/1', '2024-01-01T00:00:00Z'),
		  (2, 'Unknown Source', 'https://example.org/jobs/2', '2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert sightings: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	expectedKey, _ := config.SourceKey("https://boards.greenhouse.io/acmeco")
	var src1 string
	newDB.QueryRow(`SELECT source FROM vacancy_source WHERE vacancy_id=1`).Scan(&src1)
	if src1 != expectedKey {
		t.Errorf("id=1 source: got %q, want %q (SourceKey)", src1, expectedKey)
	}

	var src2 string
	newDB.QueryRow(`SELECT source FROM vacancy_source WHERE vacancy_id=2`).Scan(&src2)
	if src2 != "name:Unknown Source" {
		t.Errorf("id=2 source: got %q, want name:Unknown Source", src2)
	}
}

func TestImport_OwnAddressRowInsertedFirst(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (1,'co|t','T','Co','https://example.com/jobs/1',
		  't','available','2024-01-05T00:00:00Z','2024-01-05T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var src, url, seenAt string
	err = newDB.QueryRow(`SELECT source, url, seen_at FROM vacancy_source WHERE vacancy_id=1`).Scan(&src, &url, &seenAt)
	if err != nil {
		t.Fatalf("query source: %v", err)
	}
	if src != "name:import" {
		t.Errorf("source: got %q, want name:import", src)
	}
	if seenAt != "2024-01-05T00:00:00Z" {
		t.Errorf("seen_at: got %q", seenAt)
	}
}

func TestImport_SightingsDuplicateCanonicalSkipped(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (1,'co|t','T','Co','https://example.com/jobs/1',
		  't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	_, err = oldDB.Exec(`
		INSERT INTO sightings (job_id, source, url, seen)
		VALUES
		  (1, 's1', 'https://example.com/jobs/1?utm_source=a', '2024-01-01T00:00:00Z'),
		  (1, 's2', 'https://example.com/jobs/1?utm_source=b', '2024-01-02T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert sightings: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var n int
	newDB.QueryRow(`SELECT count(*) FROM vacancy_source WHERE vacancy_id=1`).Scan(&n)
	if n != 1 {
		t.Errorf("vacancy_source count: got %d, want 1", n)
	}
	if !strings.Contains(report, "skipped") {
		t.Errorf("report should mention skipped sightings")
	}
}

func TestImport_AliasesFollowed(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (10,'co|t','T','Co','https://example.com/jobs/10',
		  't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	_, err = oldDB.Exec(`INSERT INTO job_aliases (old_id, new_id) VALUES (20, 10), (30, 20)`)
	if err != nil {
		t.Fatalf("insert aliases: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var target20, target30 int64
	newDB.QueryRow(`SELECT new_id FROM vacancy_alias WHERE old_id=20`).Scan(&target20)
	newDB.QueryRow(`SELECT new_id FROM vacancy_alias WHERE old_id=30`).Scan(&target30)
	if target20 != 10 {
		t.Errorf("alias 20→: got %d, want 10", target20)
	}
	if target30 != 10 {
		t.Errorf("alias 30→: got %d, want 10", target30)
	}
}

func TestImport_AliasesBadCasesDropped(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (10,'co|t','T','Co','https://example.com/jobs/10',
		  't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	_, err = oldDB.Exec(`
		INSERT INTO job_aliases (old_id, new_id) VALUES
		  (20, 999),
		  (10, 20)`)
	if err != nil {
		t.Fatalf("insert aliases: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if !strings.Contains(report, "dropped alias id=20") {
		t.Errorf("report missing dangling alias drop: %s", report)
	}
	if !strings.Contains(report, "dropped alias id=10") {
		t.Errorf("report missing self-colliding alias drop: %s", report)
	}
}

func TestImport_LoopingAlias(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (10,'co|t','T','Co','https://example.com/jobs/10',
		  't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	_, err = oldDB.Exec(`INSERT INTO job_aliases (old_id, new_id) VALUES (20, 30), (30, 20)`)
	if err != nil {
		t.Fatalf("insert aliases: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if !strings.Contains(report, "dropped alias id=20") {
		t.Errorf("report missing looping alias drop: %s", report)
	}
}

func TestImport_TombstonesImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	const hash = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	_, err := oldDB.Exec(`INSERT INTO deleted_listings (hash) VALUES (?)`, hash)
	if err != nil {
		t.Fatalf("insert hash: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var key string
	err = newDB.QueryRow(`SELECT key FROM tombstone WHERE key=?`, "legacy:"+hash).Scan(&key)
	if err != nil {
		t.Fatalf("tombstone not found: %v", err)
	}
}

func TestImport_TombstonedAddressBlocksUpsert(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	removedURL := "https://example.com/removed/42"
	legacyHash := listing.LegacyKey(removedURL)
	_, err := oldDB.Exec(`INSERT INTO deleted_listings (hash) VALUES (?)`, legacyHash)
	if err != nil {
		t.Fatalf("insert hash: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	cat := catalog.New(newDB, time.Now)
	p := listing.Posting{
		URL:       removedURL,
		Title:     "Removed Job",
		Company:   "Co",
		Text:      "text",
		TextState: listing.TextFull,
	}
	_, outcome, err := cat.Upsert(context.Background(), p, "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if outcome != catalog.OutcomeTombstoned {
		t.Errorf("outcome: got %v, want Tombstoned", outcome)
	}
}

func TestImport_SourcesWrittenToSettingsFile(t *testing.T) {
	fromDir := makeOldDir(t, `
version: 1
sites:
  - name: Greenhouse Acme
    url: https://boards.greenhouse.io/acmeco
    enabled: true
  - name: Lever Beta
    url: https://jobs.lever.co/betacorp
    enabled: false
  - name: Never Seen
    url: https://jobs.ashbyhq.com/nosightings
    enabled: true
search: {}
preferences: {}
`)
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES
		  (1,'acme|e','E','Acme','https://boards.greenhouse.io/acmeco/jobs/1','t','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),
		  (2,'beta|e','E','Beta','https://jobs.lever.co/betacorp/abc','t','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert jobs: %v", err)
	}
	_, err = oldDB.Exec(`
		INSERT INTO sightings (job_id, source, url, seen)
		VALUES
		  (1, 'Greenhouse Acme', 'https://boards.greenhouse.io/acmeco/jobs/1', '2024-01-01T00:00:00Z'),
		  (2, 'Lever Beta', 'https://jobs.lever.co/betacorp/abc', '2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert sightings: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	store, _, err := config.Open(filepath.Join(targetDir, "sifty.yaml"))
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	cfg := store.Get()

	if len(cfg.Sources) != 2 {
		t.Errorf("sources count: got %d, want 2", len(cfg.Sources))
	}
	if cfg.Sources[0].Name != "Greenhouse Acme" {
		t.Errorf("sources[0]: got %q", cfg.Sources[0].Name)
	}
	if cfg.Sources[1].Name != "Lever Beta" {
		t.Errorf("sources[1]: got %q", cfg.Sources[1].Name)
	}
	if cfg.Sources[1].Enabled {
		t.Errorf("sources[1] enabled: got true, want false")
	}

	if !strings.Contains(report, "Never Seen") {
		t.Errorf("report missing dropped source: %s", report)
	}
	if !strings.Contains(report, "no vacancy") {
		t.Errorf("report missing 'no vacancy': %s", report)
	}
}

func TestImport_SourcesInvalidAndDuplicate(t *testing.T) {
	fromDir := makeOldDir(t, `
version: 1
sites:
  - name: Valid Source
    url: https://boards.greenhouse.io/valid
    enabled: true
  - name: Bad URL
    url: not-a-url
    enabled: true
  - name: Duplicate
    url: https://boards.greenhouse.io/valid
    enabled: true
search: {}
`)
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (1,'v|e','E','V','https://boards.greenhouse.io/valid/jobs/1','t','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	_, err = oldDB.Exec(`
		INSERT INTO sightings (job_id, source, url, seen)
		VALUES (1, 'Valid Source', 'https://boards.greenhouse.io/valid/jobs/1', '2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert sighting: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if !strings.Contains(report, `"Bad URL"`) {
		t.Errorf("report missing Bad URL: %s", report)
	}
	if !strings.Contains(report, `"Duplicate"`) {
		t.Errorf("report missing Duplicate: %s", report)
	}

	store, _, _ := config.Open(filepath.Join(targetDir, "sifty.yaml"))
	cfg := store.Get()
	if len(cfg.Sources) != 1 {
		t.Errorf("sources count: got %d, want 1", len(cfg.Sources))
	}
}

func TestImport_NonImportedConfigKeys(t *testing.T) {
	fromDir := makeOldDir(t, `
version: 1
sites: []
search:
  something: value
preferences:
  another: value
`)

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if !strings.Contains(report, "not imported") {
		t.Errorf("report missing non-imported keys notice: %s", report)
	}
	if !strings.Contains(report, "preferences") || !strings.Contains(report, "search") {
		t.Errorf("report missing key names: %s", report)
	}
}

func TestImport_LastRunWritten(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	const startTime = "2024-02-20T08:30:00Z"
	_, err := oldDB.Exec(`
		INSERT INTO collection_runs (id, started, finished, status, pid, mode, config_hash)
		VALUES
		  (1, '2024-02-15T08:00:00Z', '2024-02-15T08:05:00Z', 'done', 1, 'auto', 'abc'),
		  (2, ?, '2024-02-20T08:35:00Z', 'done', 2, 'auto', 'abc')`, startTime)
	if err != nil {
		t.Fatalf("insert runs: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var kind, subject, state string
	var attempts int
	var notBefore, created, updated string
	err = newDB.QueryRow(`SELECT kind, subject, state, attempts, not_before, created, updated FROM job WHERE kind='collect'`).Scan(
		&kind, &subject, &state, &attempts, &notBefore, &created, &updated)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}
	if kind != "collect" || state != "done" || attempts != 1 || subject != "" {
		t.Errorf("job fields: kind=%q state=%q attempts=%d subject=%q", kind, state, attempts, subject)
	}
	if notBefore != startTime {
		t.Errorf("not_before: got %q, want %q", notBefore, startTime)
	}
	if created != notBefore || updated != notBefore {
		t.Errorf("created/updated mismatch: created=%q updated=%q", created, updated)
	}

	if !strings.Contains(report, "Last run: written") {
		t.Errorf("report: %s", report)
	}
}

func TestImport_NextIDAboveAll(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES
		  (5,'co|a','A','Co','https://example.com/jobs/5','t','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),
		  (15,'co|b','B','Co','https://example.com/jobs/15','t','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert jobs: %v", err)
	}
	_, err = oldDB.Exec(`INSERT INTO job_aliases (old_id, new_id) VALUES (25, 15)`)
	if err != nil {
		t.Fatalf("insert alias: %v", err)
	}
	_, err = oldDB.Exec(`INSERT INTO id_sequence (name, value) VALUES ('jobs', 50)`)
	if err != nil {
		t.Fatalf("insert id_sequence: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	cat := catalog.New(newDB, time.Now)
	p := listing.Posting{
		URL:       "https://example.com/jobs/new",
		Title:     "New Job",
		Company:   "NewCo",
		Text:      "text",
		TextState: listing.TextFull,
	}
	newID, _, err := cat.Upsert(context.Background(), p, "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if newID <= 49 {
		t.Errorf("next id: got %d, want > 49", newID)
	}
}

func TestImport_UnknownVersionRefused(t *testing.T) {
	dir := t.TempDir()
	oldDB := makeOldDB(t, dir, oldDBFile, 42)
	_ = oldDB

	cfgDir := filepath.Join(dir, "config")
	os.MkdirAll(cfgDir, 0700)

	targetDir := filepath.Join(t.TempDir(), "new-data")
	dbPath := filepath.Join(targetDir, "sifty.db")
	settingsPath := filepath.Join(targetDir, "sifty.yaml")

	var buf bytes.Buffer
	err := importv1.Import(context.Background(), dir, dbPath, settingsPath, &buf)
	if err == nil {
		t.Fatal("expected error for wrong version, got nil")
	}
	if !strings.Contains(err.Error(), "42") {
		t.Errorf("error: %v", err)
	}
	if _, statErr := os.Stat(targetDir); !os.IsNotExist(statErr) {
		t.Errorf("target folder should not exist after failed import")
	}
}

func TestImport_NonEmptyTargetRefused(t *testing.T) {
	fromDir := makeOldDir(t, "")

	targetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetDir, "existing.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(targetDir, "sifty.db")
	settingsPath := filepath.Join(targetDir, "sifty.yaml")

	var buf bytes.Buffer
	err := importv1.Import(context.Background(), fromDir, dbPath, settingsPath, &buf)
	if err == nil {
		t.Fatal("expected error for non-empty target, got nil")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("error: %v", err)
	}
}

func TestImport_OldFileUnchangedAfterImport(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (1,'co|t','T','Co','https://example.com/jobs/1','t','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	oldDB.Close()

	oldPath := filepath.Join(fromDir, oldDBFile)
	beforeBytes, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	afterBytes, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Errorf("old database changed after import")
	}
}

func TestImport_ReportContainsCountsAndIDsOnly(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	const vacURL = "https://example.com/jobs/secret-title-company"
	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (1,'secret|title','Secret Title','Secret Company',?,
		  'secret description','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`,
		vacURL)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if strings.Contains(report, "secret") {
		t.Errorf("report contains text that should not be there: %s", report)
	}
}

func TestImport_DateWithoutZoneReadAsUTC(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, published_at, published_basis, first_seen, last_seen)
		VALUES (1,'co|t','T','Co','https://example.com/jobs/1',
		  't','available','2024-05-10T14:30:00',NULL,'2024-05-10T14:30:00','2024-05-10T14:30:00')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var firstSeen, publishedAt string
	newDB.QueryRow(`SELECT first_seen, published_at FROM vacancy WHERE id=1`).Scan(&firstSeen, &publishedAt)

	if firstSeen != "2024-05-10T14:30:00Z" {
		t.Errorf("first_seen: got %q, want 2024-05-10T14:30:00Z", firstSeen)
	}
	if publishedAt != "2024-05-10T14:30:00Z" {
		t.Errorf("published_at: got %q", publishedAt)
	}
}

func TestImport_VacancyDroppedWhenCanonicalFails(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (1,'co|t','T','Co','ftp://example.com/jobs/1',
		  't','available','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var n int
	newDB.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&n)
	if n != 0 {
		t.Errorf("vacancy count: got %d, want 0", n)
	}
	if !strings.Contains(report, "dropped id=1") {
		t.Errorf("report missing drop notice: %s", report)
	}
}

func TestImport_ExclusionsImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	oldDB.Exec(`INSERT INTO stop_words (word, created_at) VALUES ('rust', '2024-01-01T00:00:00Z')`)
	oldDB.Exec(`INSERT INTO stop_words (word, created_at) VALUES ('crypto', '2024-01-02T00:00:00Z')`)
	oldDB.Exec(`INSERT INTO block_rules (kind, value, created_at) VALUES ('company', 'Globex', '2024-01-01T00:00:00Z')`)
	oldDB.Exec(`INSERT INTO block_rules (kind, value, created_at) VALUES ('source', 'example.com', '2024-01-01T00:00:00Z')`)
	oldDB.Exec(`INSERT INTO user_profiles (id, revision, data, created_at, updated_at) VALUES ('p1', 1, '{"search":{"except":["machine learning","blockchain"]}}', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var n int
	newDB.QueryRow(`SELECT count(*) FROM exclusion`).Scan(&n)
	if n != 5 {
		t.Errorf("exclusion count: got %d, want 5", n)
	}
	var kind string
	newDB.QueryRow(`SELECT kind FROM exclusion WHERE value='Globex'`).Scan(&kind)
	if kind != "company" {
		t.Errorf("Globex: kind=%q, want company", kind)
	}
	var cnt int
	newDB.QueryRow(`SELECT count(*) FROM exclusion WHERE value='example.com'`).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("source block rule must not be imported: got %d", cnt)
	}
	var profExclCount int
	newDB.QueryRow(`SELECT count(*) FROM exclusion WHERE profile_id=1`).Scan(&profExclCount)
	if profExclCount != 2 {
		t.Errorf("per-profile exclusion count for profile 1: got %d, want 2", profExclCount)
	}
	if !strings.Contains(report, "Exclusions: 5 written") {
		t.Errorf("report missing exclusion count: %s", report)
	}
	if !strings.Contains(report, "Profile search:") {
		t.Errorf("report missing profile search section: %s", report)
	}
}

func insertJob(t *testing.T, db *sql.DB, id int64, url string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		id, "co|title", "Title", "Co", url, "t", "available",
		"2024-06-01T00:00:00Z", "2024-06-01T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("insertJob %d: %v", id, err)
	}
}

func insertProfile(t *testing.T, db *sql.DB, id, name, createdAt string, archived bool) {
	t.Helper()
	var archivedAt interface{}
	if archived {
		archivedAt = "2024-07-01T00:00:00Z"
	}
	_, err := db.Exec(
		`INSERT INTO user_profiles (id, revision, name, data, created_at, updated_at, archived_at)
		 VALUES (?,1,?,?,?,?,?)`,
		id, name, `{}`, createdAt, createdAt, archivedAt,
	)
	if err != nil {
		t.Fatalf("insertProfile %q: %v", id, err)
	}
}

func insertStage(t *testing.T, db *sql.DB, profileID string, jobID int64, status, reason, updatedAt string) {
	t.Helper()
	var r interface{}
	if reason != "" {
		r = reason
	}
	_, err := db.Exec(
		`INSERT INTO profile_job_stages (profile_id, job_id, status, outcome_reason, updated_at)
		 VALUES (?,?,?,?,?)`,
		profileID, jobID, status, r, updatedAt,
	)
	if err != nil {
		t.Fatalf("insertStage profile=%q job=%d: %v", profileID, jobID, err)
	}
}

func TestImport_ProfilesNonArchivedImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertProfile(t, oldDB, "pa", "Alice", "2024-01-01T00:00:00Z", false)
	insertProfile(t, oldDB, "pb", "Bob", "2024-02-01T00:00:00Z", false)
	insertProfile(t, oldDB, "pc", "Charlie", "2024-03-01T00:00:00Z", true)

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var n int
	newDB.QueryRow(`SELECT count(*) FROM profile`).Scan(&n)
	if n != 2 {
		t.Errorf("profile count: got %d, want 2", n)
	}

	var data1 string
	newDB.QueryRow(`SELECT data FROM profile WHERE id=1`).Scan(&data1)
	if !strings.Contains(data1, "Alice") {
		t.Errorf("profile 1 data: got %q, want Alice", data1)
	}

	var data2 string
	newDB.QueryRow(`SELECT data FROM profile WHERE id=2`).Scan(&data2)
	if !strings.Contains(data2, "Bob") {
		t.Errorf("profile 2 data: got %q, want Bob", data2)
	}

	if !strings.Contains(report, "Profiles: 3 read, 2 written, 1 dropped") {
		t.Errorf("report profiles line: %s", report)
	}
}

func TestImport_DecisionStageKinds(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertJob(t, oldDB, 1, "https://example.com/jobs/1")
	insertJob(t, oldDB, 2, "https://example.com/jobs/2")
	insertJob(t, oldDB, 3, "https://example.com/jobs/3")
	insertJob(t, oldDB, 4, "https://example.com/jobs/4")
	insertProfile(t, oldDB, "pa", "Alice", "2024-01-01T00:00:00Z", false)

	insertStage(t, oldDB, "pa", 1, "new", "", "2024-06-01T10:00:00Z")
	insertStage(t, oldDB, "pa", 2, "saved", "", "2024-06-02T10:00:00Z")
	insertStage(t, oldDB, "pa", 3, "rejected", "not a fit", "2024-06-03T10:00:00Z")
	insertStage(t, oldDB, "pa", 4, "applied", "", "2024-06-04T10:00:00Z")

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var cnt int
	newDB.QueryRow(`SELECT count(*) FROM decision WHERE vacancy=1`).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("vacancy 1 (new): want no decision, got %d", cnt)
	}

	var stage2 string
	newDB.QueryRow(`SELECT stage FROM decision WHERE vacancy=2`).Scan(&stage2)
	if stage2 != "shortlist" {
		t.Errorf("vacancy 2: got stage %q, want shortlist", stage2)
	}

	var stage3, reason3 string
	newDB.QueryRow(`SELECT stage, reason FROM decision WHERE vacancy=3`).Scan(&stage3, &reason3)
	if stage3 != "dismissed" {
		t.Errorf("vacancy 3: got stage %q, want dismissed", stage3)
	}
	if reason3 != "not a fit" {
		t.Errorf("vacancy 3: got reason %q, want 'not a fit'", reason3)
	}

	var stage4 string
	newDB.QueryRow(`SELECT stage FROM decision WHERE vacancy=4`).Scan(&stage4)
	if stage4 != "applied" {
		t.Errorf("vacancy 4: got stage %q, want applied", stage4)
	}
}

func TestImport_DecisionOnMergedVacancy(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertJob(t, oldDB, 10, "https://example.com/jobs/merge?utm_source=a")
	insertJob(t, oldDB, 20, "https://example.com/jobs/merge?utm_source=b")
	insertProfile(t, oldDB, "pa", "Alice", "2024-01-01T00:00:00Z", false)

	insertStage(t, oldDB, "pa", 20, "saved", "", "2024-06-01T00:00:00Z")

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var stage string
	err := newDB.QueryRow(`SELECT stage FROM decision WHERE vacancy=10 AND profile=1`).Scan(&stage)
	if err != nil {
		t.Fatalf("decision for kept vacancy: %v", err)
	}
	if stage != "shortlist" {
		t.Errorf("stage: got %q, want shortlist", stage)
	}

	var cnt int
	newDB.QueryRow(`SELECT count(*) FROM decision WHERE vacancy=20`).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("decision on aliased vacancy: want 0, got %d", cnt)
	}
}

func TestImport_MergedVacancyStrongestStageWins(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertJob(t, oldDB, 10, "https://example.com/jobs/merge?utm_source=a")
	insertJob(t, oldDB, 20, "https://example.com/jobs/merge?utm_source=b")
	insertProfile(t, oldDB, "pa", "Alice", "2024-01-01T00:00:00Z", false)

	insertStage(t, oldDB, "pa", 10, "rejected", "stale", "2024-06-01T00:00:00Z")
	insertStage(t, oldDB, "pa", 20, "applied", "", "2024-06-02T00:00:00Z")

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var stage string
	err := newDB.QueryRow(`SELECT stage FROM decision WHERE vacancy=10 AND profile=1`).Scan(&stage)
	if err != nil {
		t.Fatalf("decision: %v", err)
	}
	if stage != "applied" {
		t.Errorf("stage: got %q, want applied (strongest wins)", stage)
	}
}

func TestImport_ApplicationWithRecordedMoveToApplied(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertJob(t, oldDB, 1, "https://example.com/jobs/1")
	insertProfile(t, oldDB, "pa", "Alice", "2024-01-01T00:00:00Z", false)
	insertProfile(t, oldDB, "pb", "Bob", "2024-02-01T00:00:00Z", false)

	_, err := oldDB.Exec(`UPDATE jobs SET applied_at='2024-07-15T12:00:00Z' WHERE id=1`)
	if err != nil {
		t.Fatalf("set applied_at: %v", err)
	}

	_, err = oldDB.Exec(`
		INSERT INTO profile_workflow_history (profile_id, job_id, created, from_stage, to_stage)
		VALUES ('pb', 1, '2024-07-15T12:00:00Z', 'saved', 'applied')`)
	if err != nil {
		t.Fatalf("insert history: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var appVacancy int64
	var appliedAt string
	var appProfile int64
	var appProfileName string
	err = newDB.QueryRow(`SELECT vacancy, applied_at, profile, profile_name FROM application WHERE vacancy=1`).
		Scan(&appVacancy, &appliedAt, &appProfile, &appProfileName)
	if err != nil {
		t.Fatalf("query application: %v", err)
	}
	if appliedAt != "2024-07-15T12:00:00Z" {
		t.Errorf("applied_at: got %q", appliedAt)
	}
	if appProfile != 2 {
		t.Errorf("profile: got %d, want 2 (Bob)", appProfile)
	}
	if appProfileName != "Bob" {
		t.Errorf("profile_name: got %q, want Bob", appProfileName)
	}
}

func TestImport_ApplicationFallsBackToFirstProfile(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertJob(t, oldDB, 1, "https://example.com/jobs/1")
	insertProfile(t, oldDB, "pa", "Alice", "2024-01-01T00:00:00Z", false)

	_, err := oldDB.Exec(`UPDATE jobs SET applied_at='2024-08-01T00:00:00Z' WHERE id=1`)
	if err != nil {
		t.Fatalf("set applied_at: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var appProfile int64
	newDB.QueryRow(`SELECT profile FROM application WHERE vacancy=1`).Scan(&appProfile)
	if appProfile != 1 {
		t.Errorf("profile: got %d, want 1 (Alice, the first profile)", appProfile)
	}
}

func TestImport_ReadMarksImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertJob(t, oldDB, 1, "https://example.com/jobs/1")
	insertJob(t, oldDB, 2, "https://example.com/jobs/2")

	_, err := oldDB.Exec(`UPDATE jobs SET seen_at='2024-06-10T08:00:00Z' WHERE id=1`)
	if err != nil {
		t.Fatalf("set seen_at: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var seenAt1, seenAt2 string
	newDB.QueryRow(`SELECT seen_at FROM vacancy WHERE id=1`).Scan(&seenAt1)
	newDB.QueryRow(`SELECT seen_at FROM vacancy WHERE id=2`).Scan(&seenAt2)

	if seenAt1 != "2024-06-10T08:00:00Z" {
		t.Errorf("vacancy 1 seen_at: got %q, want 2024-06-10T08:00:00Z", seenAt1)
	}
	if seenAt2 != "" {
		t.Errorf("vacancy 2 seen_at: got %q, want empty", seenAt2)
	}

	if !strings.Contains(report, "Read marks: 1 before, 1 written") {
		t.Errorf("report read marks: %s", report)
	}
}

func TestImport_StageDifferenceReported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertJob(t, oldDB, 1, "https://example.com/jobs/1")
	insertJob(t, oldDB, 2, "https://example.com/jobs/2")
	insertProfile(t, oldDB, "pa", "Alice", "2024-01-01T00:00:00Z", false)

	_, err := oldDB.Exec(`UPDATE jobs SET status='saved' WHERE id=1`)
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	insertStage(t, oldDB, "pa", 1, "applied", "", "2024-06-01T00:00:00Z")

	insertStage(t, oldDB, "pa", 2, "new", "", "2024-06-01T00:00:00Z")

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if !strings.Contains(report, `vacancy id=1`) {
		t.Errorf("report missing difference for vacancy 1: %s", report)
	}
	if !strings.Contains(report, `jobs.status="saved"`) {
		t.Errorf("report missing jobs.status: %s", report)
	}
	if strings.Contains(report, `vacancy id=2`) {
		t.Errorf("report should not mention vacancy 2 (no difference): %s", report)
	}
}

func TestImport_ReadMarksMergedVacancyLatestWins(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertJob(t, oldDB, 10, "https://example.com/jobs/mark?utm_medium=email")
	insertJob(t, oldDB, 20, "https://example.com/jobs/mark?utm_medium=social")

	_, err := oldDB.Exec(`UPDATE jobs SET seen_at='2024-05-01T00:00:00Z' WHERE id=10`)
	if err != nil {
		t.Fatalf("set seen_at 10: %v", err)
	}
	_, err = oldDB.Exec(`UPDATE jobs SET seen_at='2024-06-15T00:00:00Z' WHERE id=20`)
	if err != nil {
		t.Fatalf("set seen_at 20: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var seenAt string
	newDB.QueryRow(`SELECT seen_at FROM vacancy WHERE id=10`).Scan(&seenAt)
	if seenAt != "2024-06-15T00:00:00Z" {
		t.Errorf("seen_at: got %q, want later date from aliased job", seenAt)
	}
}

func TestImport_ProfileNamesCarriedOver(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertProfile(t, oldDB, "p-m", "Meridian", "2024-01-01T00:00:00Z", false)
	insertProfile(t, oldDB, "p-s", "Solaris", "2024-02-01T00:00:00Z", false)

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var data1, data2 string
	newDB.QueryRow(`SELECT data FROM profile WHERE id=1`).Scan(&data1)
	newDB.QueryRow(`SELECT data FROM profile WHERE id=2`).Scan(&data2)

	if !strings.Contains(data1, "Meridian") {
		t.Errorf("profile 1 data: got %q, want Meridian", data1)
	}
	if !strings.Contains(data2, "Solaris") {
		t.Errorf("profile 2 data: got %q, want Solaris", data2)
	}
}

func TestImport_ProfileSearchFieldsImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	insertProfile(t, oldDB, "p1", "Helio", "2024-01-01T00:00:00Z", false)
	_, err := oldDB.Exec(`UPDATE user_profiles SET data=? WHERE id='p1'`,
		`{"targetRoles":["backend","platform"],"search":{"keywords":["distributed","observability"],"except":["gambling"]}}`)
	if err != nil {
		t.Fatalf("set p1 data: %v", err)
	}

	insertProfile(t, oldDB, "p2", "Vesper", "2024-02-01T00:00:00Z", false)
	_, err = oldDB.Exec(`UPDATE user_profiles SET data=? WHERE id='p2'`,
		`{"search":{"keywords":["ignored"],"expressions":[{"or":["golang","rust"]},{"and":["kubernetes","cloud"]}],"except":["crypto"]}}`)
	if err != nil {
		t.Fatalf("set p2 data: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var data1 string
	newDB.QueryRow(`SELECT data FROM profile WHERE id=1`).Scan(&data1)
	if !strings.Contains(data1, `"backend"`) {
		t.Errorf("profile 1 data missing role backend: %s", data1)
	}
	if !strings.Contains(data1, `"distributed"`) {
		t.Errorf("profile 1 data missing keyword distributed: %s", data1)
	}

	var data2 string
	newDB.QueryRow(`SELECT data FROM profile WHERE id=2`).Scan(&data2)
	if !strings.Contains(data2, `"golang"`) {
		t.Errorf("profile 2 data missing keyword golang: %s", data2)
	}
	if !strings.Contains(data2, `"kubernetes"`) {
		t.Errorf("profile 2 data missing allOf kubernetes: %s", data2)
	}
	if strings.Contains(data2, `"ignored"`) {
		t.Errorf("profile 2 data should not contain 'ignored' (expressions take precedence): %s", data2)
	}

	var excl1, excl2 int
	newDB.QueryRow(`SELECT count(*) FROM exclusion WHERE profile_id=1`).Scan(&excl1)
	newDB.QueryRow(`SELECT count(*) FROM exclusion WHERE profile_id=2`).Scan(&excl2)
	if excl1 != 1 {
		t.Errorf("profile 1 per-profile exclusions: got %d, want 1", excl1)
	}
	if excl2 != 1 {
		t.Errorf("profile 2 per-profile exclusions: got %d, want 1", excl2)
	}

	if !strings.Contains(report, "Profile search:") {
		t.Errorf("report missing profile search section: %s", report)
	}
	if !strings.Contains(report, "id=1 (Helio)") {
		t.Errorf("report missing profile 1 search line: %s", report)
	}
	if !strings.Contains(report, "id=2 (Vesper)") {
		t.Errorf("report missing profile 2 search line: %s", report)
	}
}

func TestImport_IndustriesReadFromBoardURL(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen, description_html)
		VALUES (1, 'chain|dev', 'Blockchain developer', 'ChainCo',
		  'https://web3.career/jobs/chaindev',
		  '', 'available', '2024-01-01T00:00:00Z', '2024-01-02T00:00:00Z',
		  '<p>Write smart contract code.</p>')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var industriesJSON string
	if err := newDB.QueryRowContext(context.Background(), `SELECT industries FROM vacancy WHERE id=1`).Scan(&industriesJSON); err != nil {
		t.Fatalf("query industries: %v", err)
	}
	if !strings.Contains(industriesJSON, "Web3") {
		t.Errorf("industries: got %q, want JSON array containing Web3", industriesJSON)
	}
}

func TestImport_VacancyFromUnknownBoardKeepsEmptyIndustries(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen,
		  description_html)
		VALUES (1, 'admin|coord', 'Administrative coordinator', 'OfficeOrg',
		  'https://example.com/jobs/admincoord',
		  '', 'available', '2024-01-01T00:00:00Z', '2024-01-02T00:00:00Z',
		  '<p>Coordinate schedules and manage correspondence.</p>')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var industriesJSON string
	if err := newDB.QueryRowContext(context.Background(), `SELECT industries FROM vacancy WHERE id=1`).Scan(&industriesJSON); err != nil {
		t.Fatalf("query industries: %v", err)
	}
	if industriesJSON != "[]" {
		t.Errorf("vacancy from unknown board: got %q, want []", industriesJSON)
	}
}

func TestImport_CompanyRecovery(t *testing.T) {
	cases := []struct {
		id                        int
		title, company, url, want string
	}{
		{1, "Engineer", "", "https://jobs.example.com/companies/fund-protocol/jobs/1-engineer", "Fund Protocol"},
		{2, "Lead", "DeFi Labs", "https://jobs.example.com/companies/defi-labs/jobs/2-lead", "DeFi Labs"},
		{3, "Dev", "", "https://jobs.example.com/companies/defi-labs/jobs/3-dev", "DeFi Labs"},
		{4, "Blockchain Dev", "", "https://weworkremotely.com/remote-jobs/acme-co-blockchain-dev", "Acme Co"},
		{5, "Blockchain Dev", "", "https://web3.career/blockchain-dev-chainlink/12345", "Chainlink"},
		{6, "Engineer", "", "https://jobs.example.com/companies/zzz-probe/jobs/6-engineer", ""},
		{7, "Designer", "Already Set Inc", "https://jobs.example.com/companies/other-org/jobs/7-designer", "Already Set Inc"},
		{8, "PM", "Acme Corp", "https://jobs.example.com/companies/acme-corp/jobs/8-pm", "Acme Corp"},
		{9, "Dev", "", "https://jobs.example.com/companies/acme-2/jobs/9-dev", "Acme"},
		{10, "Dev", "", "https://jobs.example.com/companies/acme-2-1a2b3c4d-5e6f-7890-abcd-ef1234567890/jobs/10-dev", "Acme"},
		{11, "Blockchain Dev", "", "https://weworkremotely.com/remote-jobs/acme-co-blockchain-dev-1", "Acme Co"},
		{12, "Frontend Dev", "", "https://weworkremotely.com/remote-jobs/acme-corp-different-job", "Acme Corp"},
		{13, "Frontend Dev", "", "https://weworkremotely.com/remote-jobs/unknown-vendor-some-job", ""},
		{14, "Platform Engineer [Contract] (Europe)", "", "https://weworkremotely.com/remote-jobs/orbit-labs-platform-engineer", "Orbit Labs"},
		{15, "Développeur Sénior | Données", "", "https://weworkremotely.com/remote-jobs/orbit-labs-developpeur-senior-donnees", "Orbit Labs"},
		{16, "Développeur Sénior | Données", "", "https://weworkremotely.com/remote-jobs/orbit-labs-developpeur-senior-donnees-4821", "Orbit Labs"},
	}

	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)
	for _, c := range cases {
		_, err := oldDB.Exec(
			`INSERT INTO jobs (id, identity, title, company, original_url, description, description_status, first_seen, last_seen) VALUES (?, '', ?, ?, ?, 'text', 'available', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`,
			c.id, c.title, c.company, c.url)
		if err != nil {
			t.Fatalf("insert %d: %v", c.id, err)
		}
	}
	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)
	for _, c := range cases {
		var got string
		err := newDB.QueryRow(`SELECT company FROM vacancy WHERE id=?`, c.id).Scan(&got)
		if err == sql.ErrNoRows && c.want == "" {
			continue
		}
		if err != nil {
			t.Errorf("id=%d: query: %v", c.id, err)
			continue
		}
		if got != c.want {
			t.Errorf("id=%d: company = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestImport_TextlessVacancyFilter(t *testing.T) {
	cases := []struct {
		id         int64
		url        string
		descStatus string
		desc       string
		stage      string
		appliedAt  string
		seenAt     string
		wantKept   bool
	}{
		{1, "https://example.com/jobs/1", "other_status", "some summary", "", "", "", false},
		{2, "https://example.com/jobs/2", "other_status", "some summary", "rejected", "", "", true},
		{3, "https://example.com/jobs/3", "other_status", "some summary", "", "2024-05-01T00:00:00Z", "", true},
		{4, "https://example.com/jobs/4", "other_status", "some summary", "draft", "", "", true},
		{5, "https://example.com/jobs/5", "other_status", "some summary", "", "", "2024-05-01T00:00:00Z", false},
		{6, "https://example.com/jobs/6", "blocked_by_site", "", "", "", "", false},
		{7, "https://example.com/jobs/7", "", "", "", "", "", false},
	}

	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)
	insertProfile(t, oldDB, "p1", "Tester", "2024-01-01T00:00:00Z", false)

	for _, tc := range cases {
		var appliedAt, seenAt interface{}
		if tc.appliedAt != "" {
			appliedAt = tc.appliedAt
		}
		if tc.seenAt != "" {
			seenAt = tc.seenAt
		}
		_, err := oldDB.Exec(`
			INSERT INTO jobs (id, identity, title, company, original_url,
			  description, description_status, first_seen, last_seen, applied_at, seen_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			tc.id, "co|title", "Title", "Co", tc.url,
			tc.desc, tc.descStatus, "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z",
			appliedAt, seenAt)
		if err != nil {
			t.Fatalf("insert job %d: %v", tc.id, err)
		}
		if tc.stage != "" {
			insertStage(t, oldDB, "p1", tc.id, tc.stage, "", "2024-01-01T00:00:00Z")
		}
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	for _, tc := range cases {
		var n int
		newDB.QueryRow(`SELECT count(*) FROM vacancy WHERE id=?`, tc.id).Scan(&n)
		if (n > 0) != tc.wantKept {
			t.Errorf("id=%d url=%s: present=%v, want %v", tc.id, tc.url, n > 0, tc.wantKept)
		}
	}

	droppedURL := cases[0].url
	cat := catalog.New(newDB, time.Now)
	p := listing.Posting{
		URL: droppedURL, Title: "Title", Company: "Co",
		Text: "<p>full text</p>", TextState: listing.TextFull,
	}
	_, outcome, err := cat.Upsert(context.Background(), p, "src")
	if err != nil {
		t.Fatalf("upsert after drop: %v", err)
	}
	if outcome == catalog.OutcomeTombstoned {
		t.Error("dropped textless vacancy left a tombstone")
	}

	if !strings.Contains(report, "dropped (no full text, no decision)") {
		t.Errorf("report missing textless drop summary: %s", report)
	}
	if !strings.Contains(report, "summary: 2") {
		t.Errorf("report missing summary count: %s", report)
	}
	if !strings.Contains(report, "blocked: 1") {
		t.Errorf("report missing blocked count: %s", report)
	}
	if !strings.Contains(report, "not_fetched: 1") {
		t.Errorf("report missing not_fetched count: %s", report)
	}
}

func TestImport_TextlessDuplicateWithFullTextKept(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen)
		VALUES
		  (1,'co|t','T','Co','https://example.com/jobs/dup?utm_source=a',
		   'some summary','other_status','2024-01-01T00:00:00Z','2024-01-05T00:00:00Z'),
		  (2,'co|t','T','Co','https://example.com/jobs/dup?utm_source=b',
		   '<p>full text here</p>','available','2024-01-02T00:00:00Z','2024-01-10T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert jobs: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var n int
	newDB.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&n)
	if n != 1 {
		t.Errorf("vacancy count: got %d, want 1 (kept because duplicate has full text)", n)
	}
	var state string
	newDB.QueryRow(`SELECT text_state FROM vacancy WHERE id=1`).Scan(&state)
	if state != "full" {
		t.Errorf("text_state: got %q, want full", state)
	}
}

func TestImport_ReadingsImported(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen, description_html)
		VALUES
		  (1,'acme|eng','Engineer','Acme','https://example.com/jobs/1',
		   'Salary: EUR 60,000 per year.','available','2024-01-01T00:00:00Z','2024-01-05T00:00:00Z',
		   '<p>Salary: EUR 60,000 per year. Must know Go.</p>'),
		  (2,'acme|pm','Product Manager','Acme','https://example.com/jobs/2',
		   'full-time role','available','2024-01-01T00:00:00Z','2024-01-05T00:00:00Z',
		   '<p>Product Manager, full-time role.</p>')`)
	if err != nil {
		t.Fatalf("insert jobs: %v", err)
	}

	_, err = oldDB.Exec(`
		CREATE TABLE match_vacancy_extractions (
		  job_id INTEGER NOT NULL,
		  created_at TEXT NOT NULL,
		  record TEXT NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create extractions table: %v", err)
	}

	_, err = oldDB.Exec(`
		INSERT INTO match_vacancy_extractions (job_id, created_at, record) VALUES
		  (1, '2024-02-01T10:00:00Z', '{"requirements":[{"text":"Go","kind":"skill","level":"required","quote":"Must know Go.","alternatives":null}],"conditions":{"compensation":{"value":"EUR 60,000 per year","quote":"Salary: EUR 60,000 per year."}}}'),
		  (2, '2024-02-01T10:00:00Z', '{"requirements":[{"text":"roadmapping","kind":"skill","level":"required","quote":"roadmapping experience","alternatives":["roadmap"]}],"conditions":{"engagement":{"value":"full-time","quote":"full-time role"}}}'),
		  (99, '2024-02-01T10:00:00Z', '{"requirements":[{"text":"Python","kind":"skill","level":"required","quote":"Python","alternatives":null}],"conditions":{}}')`)
	if err != nil {
		t.Fatalf("insert extractions: %v", err)
	}

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var recJSON1, readerVer1, created1 string
	var version1 int64
	if err := newDB.QueryRow(`SELECT version, reader_version, record, created FROM vacancy_reading WHERE vacancy_id=1`).
		Scan(&version1, &readerVer1, &recJSON1, &created1); err != nil {
		t.Fatalf("reading for vacancy 1: %v", err)
	}
	if version1 != 1 {
		t.Errorf("reading version: got %d, want 1", version1)
	}
	if readerVer1 != "import-1" {
		t.Errorf("reader_version: got %q, want import-1", readerVer1)
	}
	if created1 != "2024-02-01T10:00:00Z" {
		t.Errorf("created: got %q, want 2024-02-01T10:00:00Z", created1)
	}
	if !strings.Contains(recJSON1, `"kind":"skill"`) {
		t.Errorf("requirement kind not stored: %s", recJSON1)
	}

	var recJSON2 string
	if err := newDB.QueryRow(`SELECT record FROM vacancy_reading WHERE vacancy_id=2`).Scan(&recJSON2); err != nil {
		t.Fatalf("reading for vacancy 2: %v", err)
	}
	if !strings.Contains(recJSON2, `"engagement"`) {
		t.Errorf("engagement condition not stored: %s", recJSON2)
	}

	var n int
	newDB.QueryRow(`SELECT count(*) FROM vacancy_reading WHERE vacancy_id=99`).Scan(&n)
	if n != 0 {
		t.Errorf("reading for dropped vacancy 99 should not be imported: count=%d", n)
	}

	if !strings.Contains(report, "Readings: 2 written") {
		t.Errorf("report missing readings count: %s", report)
	}
}

func TestImport_ReadingQuoteLocatedAsOffset(t *testing.T) {
	fromDir := makeOldDir(t, "")
	oldDB := openOldDB(t, fromDir)

	_, err := oldDB.Exec(`
		INSERT INTO jobs (id, identity, title, company, original_url,
		  description, description_status, first_seen, last_seen, description_html)
		VALUES (1,'co|eng','Engineer','Co','https://example.com/jobs/1',
		  '','available','2024-01-01T00:00:00Z','2024-01-05T00:00:00Z',
		  '<p>Requirements: proficient in Go and distributed systems.</p>')`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	_, err = oldDB.Exec(`
		CREATE TABLE match_vacancy_extractions (
		  job_id INTEGER NOT NULL,
		  created_at TEXT NOT NULL,
		  record TEXT NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create extractions table: %v", err)
	}

	_, err = oldDB.Exec(`
		INSERT INTO match_vacancy_extractions (job_id, created_at, record) VALUES
		  (1, '2024-03-01T00:00:00Z', '{"requirements":[{"text":"Go","kind":"skill","level":"required","quote":"proficient in Go","alternatives":null},{"text":"Kafka","kind":"skill","level":"nice_to_have","quote":"not in text anywhere","alternatives":null}],"conditions":{}}')`)
	if err != nil {
		t.Fatalf("insert extraction: %v", err)
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)
	newDB := openNewDB(t, targetDir)

	var recJSON string
	if err := newDB.QueryRow(`SELECT record FROM vacancy_reading WHERE vacancy_id=1`).Scan(&recJSON); err != nil {
		t.Fatalf("reading for vacancy 1: %v", err)
	}
	if !strings.Contains(recJSON, `"start"`) {
		t.Errorf("found quote not stored as offset: %s", recJSON)
	}
	if !strings.Contains(recJSON, `"text":"not in text anywhere"`) {
		t.Errorf("unfound quote not stored as text fallback: %s", recJSON)
	}
}
