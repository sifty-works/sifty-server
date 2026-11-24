package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func queueReadings(ctx context.Context, database *db.DB, enqueue func(runner.Spec) (int64, error), store *config.Store, offline bool, readerVersion string) error {
	if offline {
		return nil
	}
	cfg := store.Get()
	if cfg.WorkProviderFor("reading") == "" {
		return nil
	}
	var active int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM job WHERE kind='read_vacancy' AND state IN ('queued','running') LIMIT 1`).Scan(&active); err != nil {
		return fmt.Errorf("queue readings: check active: %w", err)
	}
	if active > 0 {
		return nil
	}

	visSQL, visArgs, profileErr := readingVisibility(ctx, database)
	if visSQL == "" {
		return profileErr
	}
	candidates, err := catalog.ReadingCandidates(ctx, database, visSQL, visArgs, readerVersion)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		enqueue(runner.Spec{Kind: "read_vacancy", Subject: fmt.Sprintf("%d/%d", c.VacancyID, c.Version), Priority: c.Priority})
	}
	return profileErr
}

func readingVisibility(ctx context.Context, database *db.DB) (string, []any, error) {
	rows, err := database.QueryContext(ctx, `SELECT id FROM profile ORDER BY id`)
	if err != nil {
		return "", nil, fmt.Errorf("queue readings: profiles: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	clauses := []string{"EXISTS (SELECT 1 FROM decision d2 WHERE d2.vacancy=v.id)"}
	var args []any
	var failed []error
	for _, id := range ids {
		p, err := candidate.ProfileSearch(ctx, database, id)
		if err != nil {
			failed = append(failed, fmt.Errorf("queue readings: profile %d: %w", id, err))
			continue
		}
		f := candidate.Visibility(p)
		if f.SQL != "" && f.SQL != "1=0" {
			clauses = append(clauses, "("+f.SQL+")")
			args = append(args, f.Args...)
		}
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args, errors.Join(failed...)
}
