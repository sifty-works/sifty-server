package facts

import (
	"context"
	"fmt"
	"strings"

	"github.com/sifty-works/sifty-server/internal/db"
)

func Save(ctx context.Context, database *db.DB, vacancy, version int64, worker string, fs []Fact) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("facts: save %d: %w", vacancy, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM fact WHERE vacancy=? AND (version<? OR worker=?)`, vacancy, version, worker); err != nil {
		return fmt.Errorf("facts: save %d: clear: %w", vacancy, err)
	}
	for _, f := range fs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO fact (vacancy, version, worker, kind, value, flag, quote_start, quote_end, way, origin, worker_version)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			vacancy, version, worker, f.Kind, f.Value, f.Flag, f.Start, f.End, f.Source.Way, f.Source.Origin, f.WorkerVersion); err != nil {
			return fmt.Errorf("facts: save %d: %w", vacancy, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("facts: save %d: %w", vacancy, err)
	}
	return nil
}

const keySelect = `(SELECT COALESCE(group_concat(a.value), '') FROM fact a WHERE a.vacancy=v.id AND a.version=v.version AND a.kind='analysed'`

const KeySQL = keySelect + `)`

func KeySQLFor(workers ...string) string {
	return keySelect + ` AND a.worker IN ('` + strings.Join(workers, `','`) + `'))`
}

type Set struct {
	Facts    []Fact
	Analysed bool
	Key      string
}

func Load(ctx context.Context, database *db.DB, vacancies ...int64) (map[int64]Set, error) {
	out := map[int64]Set{}
	if len(vacancies) == 0 {
		return out, nil
	}
	args := make([]any, len(vacancies))
	for i, id := range vacancies {
		args[i] = id
	}
	rows, err := database.QueryContext(ctx,
		`SELECT f.vacancy, f.version, f.worker, f.kind, f.value, f.flag, f.quote_start, f.quote_end, f.way, f.origin, f.worker_version
		 FROM fact f JOIN vacancy v ON v.id=f.vacancy AND v.version=f.version
		 WHERE f.vacancy IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`) ORDER BY f.vacancy, f.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("facts: load: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.Vacancy, &f.Version, &f.Worker, &f.Kind, &f.Value, &f.Flag, &f.Start, &f.End, &f.Source.Way, &f.Source.Origin, &f.WorkerVersion); err != nil {
			return nil, fmt.Errorf("facts: load: scan: %w", err)
		}
		set := out[f.Vacancy]
		if f.Kind == Analysed {
			set.Analysed = set.Analysed || f.Source.Way != Model
			if set.Key != "" {
				set.Key += ","
			}
			set.Key += f.Value
		} else {
			set.Facts = append(set.Facts, f)
		}
		out[f.Vacancy] = set
	}
	return out, rows.Err()
}
