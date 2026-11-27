package analysis

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/runner"
)

const companyKey = `substr(v.identity, 1, instr(v.identity, '|') - 1)`

const noDomainRow = `NOT EXISTS (SELECT 1 FROM fact a WHERE a.vacancy=v.id AND a.version=v.version AND a.kind='analysed' AND a.worker=? AND a.worker_version=?)`

const publishedOrSeen = `COALESCE(NULLIF(v.published_at, ''), v.first_seen)`

type member struct {
	ID, Version   int64
	URL, ApplyURL string
	Marker        string
}

func companyVacancies(ctx context.Context, database *db.DB, subject string) ([]member, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT v.id, v.version, v.url, v.apply_url,
		        COALESCE((SELECT a.value FROM fact a WHERE a.vacancy=v.id AND a.version=v.version AND a.kind='analysed' AND a.worker=? AND a.worker_version=?), '')
		 FROM vacancy v
		 WHERE `+companyKey+`=? AND v.text_state='full' AND (v.expires_at='' OR v.expires_at>?)
		 ORDER BY `+publishedOrSeen+` DESC, v.id DESC`,
		DomainWorker, domainVersion, subject, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("analysis: company vacancies: %w", err)
	}
	defer rows.Close()
	var out []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.ID, &m.Version, &m.URL, &m.ApplyURL, &m.Marker); err != nil {
			return nil, fmt.Errorf("analysis: company vacancies: scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type cachedAnswer struct {
	Version int
	Hash    string
	Domains []Domain
}

func loadAnswer(ctx context.Context, database *db.DB, subject string) (cachedAnswer, bool, error) {
	var c cachedAnswer
	var answer string
	err := database.QueryRowContext(ctx, `SELECT version, hash, answer FROM reader_cache WHERE reader=? AND subject=?`, DomainWorker, subject).Scan(&c.Version, &c.Hash, &answer)
	if err == sql.ErrNoRows {
		return cachedAnswer{}, false, nil
	}
	if err != nil {
		return cachedAnswer{}, false, fmt.Errorf("analysis: reader cache: %w", err)
	}
	var out domainAnswer
	if err := json.Unmarshal([]byte(answer), &out); err != nil {
		return cachedAnswer{}, false, nil
	}
	c.Domains = out.Domains
	return c, true, nil
}

func saveAnswer(ctx context.Context, database *db.DB, subject, hash string, answer []Domain, now time.Time) error {
	b, err := json.Marshal(domainAnswer{Domains: answer})
	if err != nil {
		return fmt.Errorf("analysis: reader cache: %w", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT OR REPLACE INTO reader_cache (reader, subject, version, hash, answer, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		DomainWorker, subject, domainVersion, hash, string(b), now.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("analysis: reader cache: save %s: %w", subject, err)
	}
	return nil
}

func ReadSubjects(ctx context.Context, database *db.DB, eligible string, args []any) ([]string, error) {
	query := append([]any{DomainWorker, domainVersion, time.Now().UTC().Format(time.RFC3339)}, args...)
	rows, err := database.QueryContext(ctx,
		`SELECT `+companyKey+` AS k FROM vacancy v
		 WHERE v.text_state='full' AND instr(v.identity, '|') > 1 AND `+noDomainRow+`
		 AND (v.expires_at='' OR v.expires_at>?)
		 AND `+eligible+`
		 GROUP BY k ORDER BY max(`+publishedOrSeen+`) DESC, k ASC`, query...)
	if err != nil {
		return nil, fmt.Errorf("analysis: reading subjects: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("analysis: reading subjects: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func QueueReading(ctx context.Context, database *db.DB, eligible string, args []any, r *runner.Runner) error {
	subjects, err := ReadSubjects(ctx, database, eligible, args)
	if err != nil {
		return err
	}
	return enqueueNew(r, ReadKind, subjects)
}

func ReadSubjectOf(ctx context.Context, database *db.DB, vacancy int64) (string, bool, error) {
	var subject string
	var needed bool
	err := database.QueryRowContext(ctx,
		`SELECT `+companyKey+`, v.text_state='full' AND instr(v.identity, '|') > 1 AND `+noDomainRow+` FROM vacancy v WHERE v.id=?`,
		DomainWorker, domainVersion, vacancy).Scan(&subject, &needed)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("analysis: reading subject of %d: %w", vacancy, err)
	}
	return subject, needed, nil
}
