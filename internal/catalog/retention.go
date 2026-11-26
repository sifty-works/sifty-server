package catalog

import (
	"context"
	"fmt"
	"time"
)

func (c *Catalog) Removable(ctx context.Context, expiredBefore, olderThan time.Time) ([]int64, error) {
	age := ""
	if !olderThan.IsZero() {
		age = olderThan.UTC().Format(time.RFC3339)
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT id FROM vacancy
		WHERE NOT EXISTS (SELECT 1 FROM application WHERE vacancy = vacancy.id)
		  AND ((expires_at != '' AND expires_at < ?)
		    OR (? != '' AND NOT EXISTS (SELECT 1 FROM decision WHERE vacancy = vacancy.id)
		        AND COALESCE(NULLIF(published_at, ''), first_seen) < ?))
		ORDER BY id`,
		expiredBefore.UTC().Format(time.RFC3339), age, age)
	if err != nil {
		return nil, fmt.Errorf("catalog: removable: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("catalog: removable: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
