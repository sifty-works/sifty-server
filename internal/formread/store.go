package formread

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
)

const (
	StateRead        = "read"
	StateEmpty       = "empty"
	StateUnreadable  = "unreadable"
	StateUnsupported = "unsupported"
)

const Kind = "prepare"

type Stored struct {
	Form
	State  string
	Reason string
}

func Addresses(ctx context.Context, database *db.DB, vacancy int64) ([]string, error) {
	var employer, applyURL, pageURL string
	err := database.QueryRowContext(ctx,
		`SELECT COALESCE(e.url, ''), v.apply_url, v.url FROM vacancy v LEFT JOIN employer_page e ON e.vacancy=v.id WHERE v.id=?`,
		vacancy).Scan(&employer, &applyURL, &pageURL)
	if err == sql.ErrNoRows {
		return nil, apperr.NotFound{Message: "Role not found."}
	}
	if err != nil {
		return nil, fmt.Errorf("formread: addresses: %w", err)
	}
	var out []string
	for _, a := range []string{employer, applyURL, pageURL} {
		if a != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

func Load(ctx context.Context, database *db.DB, vacancy int64) (Stored, bool, error) {
	var s Stored
	err := database.QueryRowContext(ctx,
		`SELECT url, system, state, reason FROM application_form WHERE vacancy=?`, vacancy).
		Scan(&s.URL, &s.System, &s.State, &s.Reason)
	if err == sql.ErrNoRows {
		return Stored{}, false, nil
	}
	if err != nil {
		return Stored{}, false, fmt.Errorf("formread: load: %w", err)
	}
	rows, err := database.QueryContext(ctx,
		`SELECT key, label, kind, required, options FROM application_field WHERE form=? ORDER BY position`, vacancy)
	if err != nil {
		return Stored{}, false, fmt.Errorf("formread: load fields: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f Field
		var options string
		if err := rows.Scan(&f.ID, &f.Label, &f.Kind, &f.Required, &options); err != nil {
			return Stored{}, false, fmt.Errorf("formread: load fields: %w", err)
		}
		if err := json.Unmarshal([]byte(options), &f.Options); err != nil {
			return Stored{}, false, fmt.Errorf("formread: decode options: %w", err)
		}
		s.Fields = append(s.Fields, f)
	}
	return s, true, rows.Err()
}

func Save(ctx context.Context, database *db.DB, vacancy int64, f Form, state, reason string, at time.Time) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("formread: save %d: %w", vacancy, err)
	}
	defer tx.Rollback()
	if state == StateRead && len(f.Fields) == 0 {
		state = StateEmpty
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO application_form (vacancy, url, system, state, reason, read_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(vacancy) DO UPDATE SET url=excluded.url, system=excluded.system, state=excluded.state, reason=excluded.reason, read_at=excluded.read_at`,
		vacancy, f.URL, f.System, state, reason, at.UTC().Format(time.RFC3339))
	if err == nil && (state == StateRead || state == StateEmpty) {
		err = saveFields(ctx, tx, vacancy, f.Fields)
	}
	if err != nil {
		return fmt.Errorf("formread: save %d: %w", vacancy, err)
	}
	return tx.Commit()
}

func saveFields(ctx context.Context, tx *sql.Tx, vacancy int64, fields []Field) error {
	old := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT key, label FROM application_field WHERE form=?`, vacancy)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, label string
		if err := rows.Scan(&key, &label); err != nil {
			rows.Close()
			return err
		}
		old[key] = label
	}
	rows.Close()
	keys := matchKeys(old, fields)
	if _, err := tx.ExecContext(ctx, `DELETE FROM application_field WHERE form=?`, vacancy); err != nil {
		return err
	}
	for i, field := range fields {
		options, _ := json.Marshal(append([]string{}, field.Options...))
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO application_field (form, key, position, label, kind, required, options) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			vacancy, keys[i], i, field.Label, field.Kind, field.Required, string(options)); err != nil {
			return err
		}
	}
	return nil
}

func matchKeys(old map[string]string, fields []Field) []string {
	byLabel, oldCount, newCount, exact := map[string]string{}, map[string]int{}, map[string]int{}, map[string]bool{}
	for key, label := range old {
		byLabel[label] = key
		oldCount[label]++
	}
	for _, f := range fields {
		newCount[f.Label]++
		_, exact[f.ID] = old[f.ID]
	}
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.ID
		if k := byLabel[f.Label]; !exact[f.ID] && !exact[k] && oldCount[f.Label] == 1 && newCount[f.Label] == 1 {
			keys[i] = k
		}
	}
	return keys
}
