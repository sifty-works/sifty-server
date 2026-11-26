package analysis

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type Candidate struct{ Vacancy, Version int64 }

func Pending(ctx context.Context, database *db.DB, eligible string, args []any) ([]Candidate, error) {
	pairs := make([]string, len(Workers))
	query := []any{string(facts.Analysed)}
	for i, w := range Workers {
		pairs[i] = "(?,?)"
		query = append(query, w.Name(), w.Version())
	}
	query = append(query, len(Workers), time.Now().UTC().Format(time.RFC3339))
	query = append(query, args...)
	rows, err := database.QueryContext(ctx,
		`SELECT v.id, v.version FROM vacancy v
		 WHERE v.text_state='full'
		 AND (SELECT count(*) FROM fact f WHERE f.vacancy=v.id AND f.version=v.version AND f.kind=?
		      AND (f.worker, f.worker_version) IN (VALUES `+strings.Join(pairs, ",")+`)) < ?
		 AND (v.expires_at='' OR v.expires_at>?)
		 AND `+eligible+`
		 ORDER BY v.first_seen ASC, v.id ASC`, query...)
	if err != nil {
		return nil, fmt.Errorf("analysis: pending: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.Vacancy, &c.Version); err != nil {
			return nil, fmt.Errorf("analysis: pending: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func Queue(ctx context.Context, database *db.DB, eligible string, args []any, r *runner.Runner) error {
	pending, err := Pending(ctx, database, eligible, args)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	for _, j := range r.Active(Kind) {
		active[j.Subject] = true
	}
	for _, c := range pending {
		subject := Subject(c.Vacancy, c.Version)
		if active[subject] {
			continue
		}
		if _, err := r.Enqueue(runner.Spec{Kind: Kind, Subject: subject}); err != nil {
			return fmt.Errorf("analysis: queue: %w", err)
		}
	}
	return nil
}

var employerHosts = func() []string {
	var v struct {
		EmployerHosts []string `yaml:"employer_hosts"`
	}
	if err := yaml.Unmarshal(conditionsYAML, &v); err != nil {
		panic("analysis: load conditions.yaml: " + err.Error())
	}
	return v.EmployerHosts
}()

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func originOf(address, apply string) facts.Origin {
	host := hostOf(address)
	if host != "" && host == hostOf(apply) {
		return facts.Employer
	}
	for _, h := range employerHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return facts.Employer
		}
	}
	return facts.Aggregator
}
