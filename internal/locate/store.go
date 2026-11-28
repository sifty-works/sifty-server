package locate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
)

func load(ctx context.Context, database *db.DB, id int64) (v Vacancy, found, exists bool, err error) {
	var page string
	err = database.QueryRowContext(ctx,
		`SELECT v.id, v.title, v.company, v.url, v.apply_url, COALESCE(e.url,'')
		 FROM vacancy v LEFT JOIN employer_page e ON e.vacancy=v.id WHERE v.id=?`, id).
		Scan(&v.ID, &v.Title, &v.Company, &v.URL, &v.ApplyURL, &page)
	if errors.Is(err, sql.ErrNoRows) {
		return Vacancy{}, false, false, nil
	}
	if err != nil {
		return Vacancy{}, false, false, fmt.Errorf("locate: load vacancy: %w", err)
	}
	return v, page != "", true, nil
}

func save(ctx context.Context, database *db.DB, id int64, l Link, now time.Time) error {
	_, err := database.ExecContext(ctx,
		`INSERT INTO employer_page (vacancy, url, system, way, searched_at)
		 SELECT id, ?, ?, ?, ? FROM vacancy WHERE id=?
		 ON CONFLICT(vacancy) DO UPDATE SET url=excluded.url, system=excluded.system, way=excluded.way, searched_at=excluded.searched_at`,
		l.URL, l.System, l.Way, now.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("locate: save: %w", err)
	}
	return nil
}

func Unsearched(ctx context.Context, database *db.DB) ([]int64, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT DISTINCT d.vacancy FROM decision d
		 WHERE d.stage='shortlist'
		   AND NOT EXISTS (SELECT 1 FROM employer_page e WHERE e.vacancy=d.vacancy)
		   AND NOT EXISTS (SELECT 1 FROM job j WHERE j.kind=? AND j.subject=CAST(d.vacancy AS TEXT))
		 ORDER BY d.vacancy`, Kind)
	if err != nil {
		return nil, fmt.Errorf("locate: unsearched: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("locate: unsearched: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
