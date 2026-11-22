package collect

import (
	"errors"
	"time"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/source"
)

type sourceRunRec struct {
	started time.Time
	status  string
	found   int
	newCnt  int
	refused int
	errText string
}

func sourceRunStatus(listErr error, found int) (string, string) {
	if listErr != nil {
		if errors.Is(listErr, source.ErrNothingRecognised) {
			return "error", "Nothing recognised on the page"
		}
		return "error", fetch.UserText(listErr)
	}
	if found == 0 {
		return "empty", ""
	}
	return "ok", ""
}

func (c *Collector) writeSourceRun(jobID int64, key string, started, finished time.Time,
	status, errText string, found, newCount, updated, unchanged, excluded, removed, refused int) {
	_, err := c.db.Exec(
		`INSERT OR REPLACE INTO source_run
		 (job_id, source, started, finished, status, found, new, updated, unchanged, excluded, removed, refused, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, key,
		started.UTC().Format(time.RFC3339), finished.UTC().Format(time.RFC3339),
		status, found, newCount, updated, unchanged, excluded, removed, refused, errText,
	)
	if err != nil {
		c.log.Warn("collect: write source_run", "key", key, "error", err)
	}
}

func (c *Collector) querySourceRuns(key string) []sourceRunRec {
	rows, err := c.db.Query(
		`SELECT started, status, found, new, refused, error
		 FROM source_run WHERE source = ? ORDER BY job_id DESC`,
		key,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []sourceRunRec
	for rows.Next() {
		var r sourceRunRec
		var startedStr string
		if err := rows.Scan(&startedStr, &r.status, &r.found, &r.newCnt, &r.refused, &r.errText); err != nil {
			continue
		}
		r.started, _ = time.Parse(time.RFC3339, startedStr)
		out = append(out, r)
	}
	return out
}

func (c *Collector) attentionSources() map[string]bool {
	rows, err := c.db.Query(`SELECT source FROM (SELECT source, status, ROW_NUMBER() OVER (PARTITION BY source ORDER BY started DESC) rn FROM source_run) WHERE rn<=3 GROUP BY source HAVING COUNT(*)=3 AND SUM(status='ok')=0`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var key string
		if rows.Scan(&key) == nil {
			out[key] = true
		}
	}
	return out
}

func needsAttention(history []sourceRunRec) bool {
	if len(history) < 3 {
		return false
	}
	for _, r := range history[:3] {
		if r.status == "ok" {
			return false
		}
	}
	return true
}
