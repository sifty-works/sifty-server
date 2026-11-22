package inbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
)

const notExcluded = catalog.NotExcluded
const notExcludedFor = catalog.NotExcludedFor

type exclRow struct {
	id          int64
	kind, value string
}

func normFor(kind, value string) string {
	if kind == "company" {
		return strings.TrimSuffix(listing.Identity(value, ""), "|")
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func queryExclusions(ctx context.Context, database *db.DB, profileID int64) ([]exclRow, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT id,kind,value FROM exclusion WHERE profile_id=? ORDER BY id DESC`,
		profileID)
	if err != nil {
		return nil, fmt.Errorf("inbox: list exclusions: %w", err)
	}
	defer rows.Close()
	var out []exclRow
	for rows.Next() {
		var r exclRow
		if err := rows.Scan(&r.id, &r.kind, &r.value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func addExclusion(ctx context.Context, database *db.DB, kind, value string, profileID int64) ([]exclRow, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, apperr.Invalid{Message: "Value must not be empty."}
	}
	if len([]rune(value)) > 200 {
		return nil, apperr.Invalid{Message: "Value must be at most 200 characters."}
	}
	if kind != "word" && kind != "company" {
		return nil, apperr.Invalid{Message: "Kind must be word or company."}
	}
	if kind == "company" && profileID != 0 {
		return nil, apperr.Invalid{Message: "Company exclusions apply to all profiles."}
	}
	_, err := database.ExecContext(ctx,
		`INSERT OR IGNORE INTO exclusion (kind,value,norm,profile_id,created) VALUES (?,?,?,?,?)`,
		kind, value, normFor(kind, value), profileID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("inbox: add exclusion: %w", err)
	}
	return queryExclusions(ctx, database, profileID)
}

func removeExclusion(ctx context.Context, database *db.DB, id int64, profileID int64) ([]exclRow, error) {
	if _, err := database.ExecContext(ctx, `DELETE FROM exclusion WHERE id=?`, id); err != nil {
		return nil, fmt.Errorf("inbox: remove exclusion: %w", err)
	}
	return queryExclusions(ctx, database, profileID)
}

func previewCount(ctx context.Context, database *db.DB, kind, value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" || (kind != "word" && kind != "company") {
		return 0, nil
	}
	norm := normFor(kind, value)

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("inbox: preview: begin tx: %w", err)
	}
	defer tx.Rollback()

	var before int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM vacancy WHERE `+notExcluded).Scan(&before); err != nil {
		return 0, fmt.Errorf("inbox: preview: count before: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO exclusion (kind,value,norm,profile_id,created) VALUES (?,?,?,0,'preview')`,
		kind, value, norm); err != nil {
		return 0, fmt.Errorf("inbox: preview: insert: %w", err)
	}
	var after int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM vacancy WHERE `+notExcluded).Scan(&after); err != nil {
		return 0, fmt.Errorf("inbox: preview: count after: %w", err)
	}
	return before - after, nil
}

type ExclusionKind string

const (
	KindWord    ExclusionKind = "word"
	KindCompany ExclusionKind = "company"
)

type Exclusion struct {
	ID    int64         `json:"id"`
	Kind  ExclusionKind `json:"kind"`
	Value string        `json:"value"`
	Scope string        `json:"scope"`
}

type ExclusionsResp struct {
	Exclusions []Exclusion `json:"exclusions"`
}

func toExclusionsResp(rows []exclRow, profileID int64) ExclusionsResp {
	list := make([]Exclusion, len(rows))
	scope := "All profiles"
	if profileID != 0 {
		scope = "This profile"
	}
	for i, r := range rows {
		list[i] = Exclusion{ID: r.id, Kind: ExclusionKind(r.kind), Value: r.value, Scope: scope}
	}
	return ExclusionsResp{Exclusions: list}
}
