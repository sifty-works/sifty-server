package main

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/locate"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/source"
)

var boardRoots = map[string]string{
	"greenhouse": "https://boards.greenhouse.io/",
	"lever":      "https://jobs.lever.co/",
	"ashby":      "https://jobs.ashbyhq.com/",
}

type boardLists struct{ fetcher fetch.Fetcher }

func (b boardLists) Jobs(ctx context.Context, system, board string) ([]locate.Job, error) {
	root, ok := boardRoots[system]
	if !ok {
		return nil, nil
	}
	u, err := url.Parse(root + board)
	if err != nil {
		return nil, nil
	}
	mod, site := source.For(u)
	site.TitlesOnly = true
	postings, err := mod.List(ctx, b.fetcher, site)
	if errors.Is(err, source.ErrNothingRecognised) {
		return nil, fetch.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	jobs := make([]locate.Job, 0, len(postings))
	for _, p := range postings {
		address := p.ApplyURL
		if address == "" {
			address = p.URL
		}
		if _, canon, ok := source.Posting(address); ok {
			address = canon
		}
		jobs = append(jobs, locate.Job{Title: p.Title, Company: p.Company, URL: address})
	}
	return jobs, nil
}

func (boardLists) Posting(raw string) (string, string, bool) { return source.Posting(raw) }

func (boardLists) Norm(s string) string { return strings.TrimSpace(listing.FTSNorm(s)) }

func queueUnsearched(ctx context.Context, database *db.DB, r *runner.Runner, log *slog.Logger) {
	ids, err := locate.Unsearched(ctx, database)
	if err != nil {
		log.Warn("employer pages not queued", "err", err)
		return
	}
	for _, id := range ids {
		if _, err := r.Enqueue(runner.Spec{Kind: locate.Kind, Subject: locate.Subject(id)}); err != nil {
			log.Warn("employer page not queued", "vacancy", id, "err", err)
		}
	}
}
