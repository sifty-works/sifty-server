package inbox

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/listing"
)

type repoRow struct {
	id             int64
	company        string
	title          string
	location       string
	publishedAt    string
	publishedBasis string
	unread         bool
	stage          string
	reason         string
	appliedAt      string
	appliedName    string
	expiresAt      string
	fit            fit.Stored
}

const listCols = `v.id, v.company, v.title, v.location, v.published_at, v.published_basis,
    v.seen_at='' AS unread, COALESCE(d.reason,'') AS reason,
    (SELECT applied_at FROM application WHERE vacancy=v.id ORDER BY applied_at DESC LIMIT 1),
    (SELECT profile_name FROM application WHERE vacancy=v.id ORDER BY applied_at DESC LIMIT 1),
    v.expires_at, ` + fit.Columns

const orderNewest = `ORDER BY published_at DESC, first_seen DESC, id DESC`

const orderDecided = `ORDER BY d.decided_at DESC, d.vacancy DESC`

func resolveProfile(ctx context.Context, database *db.DB, pid int64) (int64, error) {
	if pid == 0 {
		return candidate.ActiveProfileID(ctx, database)
	}
	var id int64
	err := database.QueryRowContext(ctx, `SELECT id FROM profile WHERE id=?`, pid).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, apperr.NotFound{Message: "Profile not found."}
	}
	if err != nil {
		return 0, fmt.Errorf("inbox: resolve profile: %w", err)
	}
	return id, nil
}

func resolveVacancy(ctx context.Context, database *db.DB, id int64) (int64, error) {
	var realID int64
	err := database.QueryRowContext(ctx,
		`SELECT id FROM vacancy WHERE id=COALESCE((SELECT new_id FROM vacancy_alias WHERE old_id=?),?)`,
		id, id).Scan(&realID)
	if err == sql.ErrNoRows {
		return 0, apperr.NotFound{Message: "Role not found."}
	}
	if err != nil {
		return 0, fmt.Errorf("inbox: resolve vacancy: %w", err)
	}
	return realID, nil
}

type SourceCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

const notExpiredSQL = `(v.expires_at='' OR v.expires_at>?)`

const remoteSQL = `(` +
	`instr(' '||lower(replace(replace(v.location,'-',' '),char(183),' '))||' ',' remote ')>0 OR ` +
	`instr(' '||lower(replace(replace(v.location,'-',' '),char(183),' '))||' ',' anywhere ')>0 OR ` +
	`instr(' '||lower(replace(replace(v.location,'-',' '),char(183),' '))||' ',' telecommute ')>0` +
	`)`

func leadershipClause() (string, []any) {
	mk := func(term string) string {
		norm := listing.FTSNorm(term)
		return `title: "` + strings.ReplaceAll(norm, `"`, `""`) + `"`
	}
	leadMatch := mk("lead")
	leadGenMatch := mk("lead generation")
	args := []any{mk("CTO"), mk("Head"), mk("Director"), mk("Manager"), leadMatch, leadGenMatch}
	sql := `(` +
		`v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?) OR ` +
		`v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?) OR ` +
		`v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?) OR ` +
		`v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?) OR ` +
		`(v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?) AND ` +
		`v.id NOT IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?))` +
		`)`
	return sql, args
}

func filterClauses(now time.Time, f Filters, skipSources bool) ([]string, []any) {
	var parts []string
	var args []any

	if f.Published != 0 {
		cutoff := now.AddDate(0, 0, -f.Published).UTC().Format(time.RFC3339)
		parts = append(parts, "(v.published_at='' OR v.published_at>=?)")
		args = append(args, cutoff)
	}

	if f.Remote {
		parts = append(parts, remoteSQL)
	}

	if f.Leadership {
		lSQL, lArgs := leadershipClause()
		parts = append(parts, lSQL)
		args = append(args, lArgs...)
	}

	if !skipSources && len(f.Sources) > 0 {
		ph := make([]string, len(f.Sources))
		for i, s := range f.Sources {
			ph[i] = "?"
			args = append(args, s)
		}
		parts = append(parts, "v.id IN (SELECT vacancy_id FROM vacancy_source WHERE source IN ("+strings.Join(ph, ",")+"))")
	}

	if f.Unread {
		parts = append(parts, "v.seen_at=''")
	}

	if b, bArgs := fit.Buckets(f.Fit, f.inputs); b != "" {
		parts = append(parts, b)
		args = append(args, bArgs...)
	}

	return parts, args
}

func scopeClause(now time.Time, profileID int64, stage, q string, vis candidate.Filter, f Filters, skipSources bool) (join, where string, args []any) {
	isInbox := stage == "inbox"
	low := strings.ToLower(strings.TrimSpace(q))

	if isInbox {
		join = `LEFT JOIN decision d ON d.vacancy=v.id AND d.profile=? ` + fit.Join
		where = `d.vacancy IS NULL AND ` + notExcludedFor
		args = []any{profileID, profileID, profileID}
		if vis.SQL != "" {
			where += ` AND (` + vis.SQL + `)`
			args = append(args, vis.Args...)
		}
	} else {
		join = `JOIN decision d ON d.vacancy=v.id AND d.profile=? AND d.stage=? ` + fit.Join
		where = notExcludedFor
		args = []any{profileID, stage, profileID, profileID}
	}

	if low != "" {
		if len([]rune(q)) >= 3 {
			normQ := strings.TrimSpace(listing.FTSNorm(q))
			if normQ != "" {
				where += ` AND (instr(lower(v.title),?)>0 OR instr(lower(v.company),?)>0 OR instr(lower(v.tags),?)>0 OR v.id IN (SELECT rowid FROM vacancy_fts WHERE vacancy_fts MATCH ?))`
				args = append(args, low, low, low, `text: "`+strings.ReplaceAll(normQ, `"`, `""`)+`"`)
			} else {
				where += ` AND (instr(lower(v.title),?)>0 OR instr(lower(v.company),?)>0 OR instr(lower(v.tags),?)>0)`
				args = append(args, low, low, low)
			}
		} else {
			where += ` AND (instr(lower(v.title),?)>0 OR instr(lower(v.company),?)>0 OR instr(lower(v.tags),?)>0)`
			args = append(args, low, low, low)
		}
	}

	fParts, fArgs := filterClauses(now, f, skipSources)
	for _, p := range fParts {
		where += ` AND ` + p
	}
	args = append(args, fArgs...)

	if stage != "applied" {
		where += ` AND ` + notExpiredSQL
		args = append(args, now.UTC().Format(time.RFC3339))
	}

	return join, where, args
}

func sourceCounts(ctx context.Context, database *db.DB, now time.Time, profileID int64, stage, q string, vis candidate.Filter, f Filters) ([]SourceCount, error) {
	join, where, args := scopeClause(now, profileID, stage, q, vis, f, true)

	rows, err := database.QueryContext(ctx, `
		SELECT vs.source, COUNT(DISTINCT v.id)
		FROM vacancy v
		`+join+`
		JOIN vacancy_source vs ON vs.vacancy_id=v.id
		WHERE `+where+`
		GROUP BY vs.source
		ORDER BY vs.source`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("inbox: source counts: %w", err)
	}
	defer rows.Close()
	var out []SourceCount
	for rows.Next() {
		var sc SourceCount
		if err := rows.Scan(&sc.Name, &sc.Count); err != nil {
			return nil, fmt.Errorf("inbox: source counts scan: %w", err)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func stageCounts(ctx context.Context, database *db.DB, now time.Time, profileID int64, vis candidate.Filter) (map[string]int, error) {
	out := map[string]int{}
	nowStr := now.UTC().Format(time.RFC3339)

	decidedRows, err := database.QueryContext(ctx, `
		SELECT d.stage, COUNT(*)
		FROM vacancy v
		JOIN decision d ON d.vacancy=v.id AND d.profile=?
		WHERE `+notExcludedFor+` AND (d.stage='applied' OR `+notExpiredSQL+`)
		GROUP BY d.stage`,
		profileID, profileID, nowStr)
	if err != nil {
		return nil, fmt.Errorf("inbox: counts decided: %w", err)
	}
	defer decidedRows.Close()
	for decidedRows.Next() {
		var s string
		var n int
		if err := decidedRows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("inbox: counts scan: %w", err)
		}
		out[s] = n
	}
	if err := decidedRows.Err(); err != nil {
		return nil, fmt.Errorf("inbox: counts decided: %w", err)
	}

	y, mo, d := now.Local().Date()
	midnight := time.Date(y, mo, d, 0, 0, 0, 0, time.Local).UTC().Format(time.RFC3339)
	inboxArgs := append([]any{midnight, profileID, profileID}, vis.Args...)
	inboxArgs = append(inboxArgs, nowStr)
	var inboxCount, newToday int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(v.first_seen >= ?), 0)
		FROM vacancy v
		LEFT JOIN decision d ON d.vacancy=v.id AND d.profile=?
		WHERE d.vacancy IS NULL AND `+notExcludedFor+` AND (`+vis.SQL+`) AND `+notExpiredSQL,
		inboxArgs...).Scan(&inboxCount, &newToday); err != nil {
		return nil, fmt.Errorf("inbox: counts inbox: %w", err)
	}
	out["inbox"] = inboxCount
	out["newToday"] = newToday

	return out, nil
}

func countRows(ctx context.Context, database *db.DB, now time.Time, profileID int64, stage, q string, vis candidate.Filter, f Filters) (int, error) {
	join, where, args := scopeClause(now, profileID, stage, q, vis, f, false)
	var n int
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vacancy v `+join+` WHERE `+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("inbox: count matching: %w", err)
	}
	return n, nil
}

func listRows(ctx context.Context, database *db.DB, now time.Time, profileID int64, stage, q, sort string, vis candidate.Filter, f Filters, limit, offset int) ([]repoRow, error) {
	join, where, args := scopeClause(now, profileID, stage, q, vis, f, false)
	order := orderDecided
	switch {
	case stage != "inbox" && stage != "shortlist":
	case sort == "newest":
		order = orderNewest
	default:
		order = fit.OrderBestFit
		args = append(args, f.inputs)
	}

	sqlRows, err := database.QueryContext(ctx,
		`SELECT `+listCols+` FROM vacancy v `+join+` WHERE `+where+` `+order+` LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("inbox: list: %w", err)
	}
	return scanListRows(stage, sqlRows)
}

func scanListRows(stage string, sqlRows *sql.Rows) ([]repoRow, error) {
	defer sqlRows.Close()
	var out []repoRow
	for sqlRows.Next() {
		var row repoRow
		var unread int64
		var appliedAt, appliedName sql.NullString
		if err := sqlRows.Scan(append([]any{
			&row.id, &row.company, &row.title, &row.location,
			&row.publishedAt, &row.publishedBasis,
			&unread, &row.reason,
			&appliedAt, &appliedName, &row.expiresAt}, row.fit.Dest()...)...,
		); err != nil {
			return nil, fmt.Errorf("inbox: scan: %w", err)
		}
		row.unread = unread != 0
		row.stage = stage
		row.appliedAt = appliedAt.String
		row.appliedName = appliedName.String
		out = append(out, row)
	}
	return out, sqlRows.Err()
}

type viewData struct {
	url       string
	applyURL  string
	textState string
	body      []byte
	sources   []string
}

func loadVacancyFull(ctx context.Context, database *db.DB, profileID, vacancyID int64) (stage string, row repoRow, vd viewData, err error) {
	var unread int64
	var appliedAt, appliedName sql.NullString
	err = database.QueryRowContext(ctx, `
		SELECT COALESCE(d.stage,'inbox'),
		       v.id, v.company, v.title, v.location, v.published_at, v.published_basis,
		       v.seen_at='' AS unread, COALESCE(d.reason,'') AS reason,
		       (SELECT applied_at FROM application WHERE vacancy=v.id ORDER BY applied_at DESC LIMIT 1),
		       (SELECT profile_name FROM application WHERE vacancy=v.id ORDER BY applied_at DESC LIMIT 1),
		       v.expires_at,
		       v.url, v.apply_url, v.text_state, v.body, `+fit.Columns+`
		FROM vacancy v LEFT JOIN decision d ON d.vacancy=v.id AND d.profile=? `+fit.Join+`
		WHERE v.id=? AND `+notExcludedFor,
		profileID, profileID, vacancyID, profileID).Scan(append([]any{
		&stage,
		&row.id, &row.company, &row.title, &row.location, &row.publishedAt, &row.publishedBasis,
		&unread, &row.reason,
		&appliedAt, &appliedName,
		&row.expiresAt,
		&vd.url, &vd.applyURL, &vd.textState, &vd.body}, row.fit.Dest()...)...)
	if err == sql.ErrNoRows {
		return "", repoRow{}, viewData{}, apperr.NotFound{Message: "Role not found."}
	}
	if err != nil {
		return "", repoRow{}, viewData{}, fmt.Errorf("inbox: vacancy full: %w", err)
	}
	row.unread = unread != 0
	row.stage = stage
	row.appliedAt = appliedAt.String
	row.appliedName = appliedName.String

	rows2, err2 := database.QueryContext(ctx,
		`SELECT source FROM vacancy_source WHERE vacancy_id=? ORDER BY rowid`, vacancyID)
	if err2 != nil {
		return "", repoRow{}, viewData{}, fmt.Errorf("inbox: vacancy sources: %w", err2)
	}
	defer rows2.Close()
	for rows2.Next() {
		var src string
		if err2 := rows2.Scan(&src); err2 != nil {
			return "", repoRow{}, viewData{}, fmt.Errorf("inbox: vacancy sources scan: %w", err2)
		}
		vd.sources = append(vd.sources, src)
	}
	return stage, row, vd, rows2.Err()
}
