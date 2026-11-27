package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const poolSize = 4

//go:embed all:migrations
var embedded embed.FS

var Migrations fs.FS

func init() {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		panic("db: cannot sub migrations FS: " + err.Error())
	}
	Migrations = sub
}

type DB struct {
	*sql.DB
	Version int
}

func Open(path string, migrations fs.FS) (*DB, error) {
	all, err := listMigrations(migrations)
	if err != nil {
		return nil, fmt.Errorf("db: listing migrations: %w", err)
	}

	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	sqldb.SetMaxOpenConns(1)

	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("db: ping %s: %w", path, err)
	}

	version, err := userVersion(sqldb)
	if err != nil {
		sqldb.Close()
		return nil, err
	}

	maxVersion := 0
	if len(all) > 0 {
		maxVersion = all[len(all)-1].version
	}

	if version > maxVersion {
		sqldb.Close()
		return nil, fmt.Errorf("this database was written by a newer version of Sifty. Install that version or newer")
	}

	pending := pendingMigrations(all, version)
	if len(pending) == 0 {
		return pooled(sqldb, version), nil
	}

	isNew := version == 0 && dbIsEmpty(sqldb)
	if !isNew {
		backupsDir := filepath.Join(filepath.Dir(path), "backups")
		if err := os.MkdirAll(backupsDir, 0700); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("db: creating backups dir: %w", err)
		}
		ts := time.Now().UTC().Format("20060102T150405Z")
		backupPath := filepath.Join(backupsDir,
			fmt.Sprintf("sifty-%s-v%d.db", ts, version))
		if err := vacuumInto(sqldb, backupPath); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("db: backup before migration: %w", err)
		}
		if err := pruneBackups(backupsDir, 5); err != nil {
			_ = err
		}
	}

	for _, m := range pending {
		if err := runMigration(sqldb, m, migrations); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("db: migration %d: %w", m.version, err)
		}
		version = m.version
	}

	return pooled(sqldb, version), nil
}

func pooled(sqldb *sql.DB, version int) *DB {
	sqldb.SetMaxOpenConns(poolSize)
	sqldb.SetMaxIdleConns(poolSize)
	return &DB{DB: sqldb, Version: version}
}

type migration struct {
	version  int
	filename string
}

var migrationRE = regexp.MustCompile(`^(\d{3,})_.*\.sql$`)

func listMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		out = append(out, migration{version: n, filename: e.Name()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("migrations %s and %s share the number %d", out[i-1].filename, out[i].filename, out[i].version)
		}
	}
	return out, nil
}

func pendingMigrations(all []migration, current int) []migration {
	var out []migration
	for _, m := range all {
		if m.version > current {
			out = append(out, m)
		}
	}
	return out
}

func runMigration(db *sql.DB, m migration, fsys fs.FS) error {
	f, err := fsys.Open(m.filename)
	if err != nil {
		return fmt.Errorf("open %s: %w", m.filename, err)
	}
	sqlBytes, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("read %s: %w", m.filename, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	sqlText := strings.TrimSpace(string(sqlBytes))
	if sqlText != "" {
		if _, err := tx.Exec(sqlText); err != nil {
			return fmt.Errorf("exec %s: %w", m.filename, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

func userVersion(db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("db: reading user_version: %w", err)
	}
	return v, nil
}

func dbIsEmpty(db *sql.DB) bool {
	var n int
	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`,
	).Scan(&n)
	return err == nil && n == 0
}

func vacuumInto(db *sql.DB, dst string) error {
	_, err := db.Exec("VACUUM INTO ?", dst)
	return err
}

func CopyReadOnly(path, dst string) error {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	sqldb, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fmt.Errorf("db: open %s: %w", path, err)
	}
	defer sqldb.Close()
	sqldb.SetMaxOpenConns(1)
	if err := vacuumInto(sqldb, dst); err != nil {
		return fmt.Errorf("db: copying %s: %w", path, err)
	}
	return nil
}

func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "sifty-") && strings.HasSuffix(e.Name(), ".db") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	for len(files) > keep {
		if err := os.Remove(files[0]); err != nil && !os.IsNotExist(err) {
			return err
		}
		files = files[1:]
	}
	return nil
}
