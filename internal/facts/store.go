package facts

import (
	"context"
	"fmt"

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

func Load(ctx context.Context, database *db.DB, vacancy, version int64) ([]Fact, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT worker, kind, value, flag, quote_start, quote_end, way, origin, worker_version
		 FROM fact WHERE vacancy=? AND version=? AND kind != ? ORDER BY id`, vacancy, version, Analysed)
	if err != nil {
		return nil, fmt.Errorf("facts: load %d: %w", vacancy, err)
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		f := Fact{Vacancy: vacancy, Version: version}
		if err := rows.Scan(&f.Worker, &f.Kind, &f.Value, &f.Flag, &f.Start, &f.End, &f.Source.Way, &f.Source.Origin, &f.WorkerVersion); err != nil {
			return nil, fmt.Errorf("facts: load %d: scan: %w", vacancy, err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
