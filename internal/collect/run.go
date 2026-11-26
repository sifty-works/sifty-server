package collect

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/source"
)

func (c *Collector) Due(now time.Time) bool {
	cfg := c.store.Get()
	if !cfg.Collection.Auto {
		return false
	}
	if last, ok := c.runner.Last("collect"); ok {
		if now.Before(last.Created.Add(time.Duration(cfg.Collection.Interval))) {
			return false
		}
	}
	return true
}

func (c *Collector) Ticker(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if !c.blocked && c.Due(now) {
				c.runner.Enqueue(runner.Spec{Kind: "collect"})
			}
			if c.OnTick != nil {
				c.OnTick(now)
			}
		}
	}
}

func (c *Collector) run(ctx context.Context, j *runner.Job) error {
	cfg := c.store.Get()
	var enabled []config.SourceConfig
	for _, s := range cfg.Sources {
		if s.Enabled {
			enabled = append(enabled, s)
		}
	}

	j.Progress(0, len(enabled), "")
	if len(enabled) == 0 {
		return nil
	}

	type grp struct{ sources []config.SourceConfig }
	var groups []grp
	groupIdx := make(map[string]int)
	for _, s := range enabled {
		u, err := url.Parse(s.URL)
		if err != nil {
			continue
		}
		h := source.GroupKey(u)
		if i, ok := groupIdx[h]; ok {
			groups[i].sources = append(groups[i].sources, s)
		} else {
			groupIdx[h] = len(groups)
			groups = append(groups, grp{[]config.SourceConfig{s}})
		}
	}

	var oldest time.Time
	if m := cfg.Collection.MaxAgeMonths; m > 0 {
		oldest = c.now().AddDate(0, -m, 0)
	}
	seenURLs := make(map[string]bool)
	var mu sync.Mutex
	var doneCount, failedCount int

	var wg sync.WaitGroup
	for _, g := range groups {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := j.Lane(ctx, "net")
			if err != nil {
				return
			}
			defer rel()

			for _, s := range g.sources {
				if ctx.Err() != nil {
					return
				}
				current := s.Name
				if current == "" {
					if u, _ := url.Parse(s.URL); u != nil {
						current = u.Host
					}
				}

				mu.Lock()
				j.Progress(doneCount, len(enabled), current)
				mu.Unlock()

				key, _ := config.SourceKey(s.URL)
				srcStarted := c.now()
				postings, listErr := c.listSource(ctx, s)

				var cntNew, cntUpdated, cntUnchanged, cntRemoved, cntExcluded, cntRefused int
				if listErr == nil {
					for _, p := range postings {
						canon, err := listing.Canonical(p.URL)
						if err != nil {
							continue
						}
						mu.Lock()
						skip := seenURLs[canon]
						if !skip {
							seenURLs[canon] = true
						}
						mu.Unlock()
						if skip {
							continue
						}
						k, knownErr := c.catalog.Known(ctx, p)
						if knownErr != nil {
							c.log.Warn("collect: known failed", "url", p.URL, "error", knownErr)
							continue
						}
						if k.Removed {
							cntRemoved++
							continue
						}
						if k.New && !p.Published.IsZero() && p.Published.Before(oldest) {
							continue
						}
						excluded := false
						if excl, exclErr := catalog.Excluded(ctx, c.db, p.Title, p.Company, p.ApplyURL); exclErr != nil {
							c.log.Warn("collect: exclusion check", "error", exclErr)
						} else {
							excluded = excl
						}
						noCompany := p.Company == "" && (k.New || !k.HasCompany)
						if !excluded {
							askAgain := k.New || k.Requested.IsZero() || c.now().Sub(k.Requested) > 7*24*time.Hour
							needDetail := (p.Text == "" && (k.New || (!k.HasText && askAgain))) ||
								(noCompany && askAgain)
							if needDetail {
								p, excluded = c.addVacancyPage(ctx, p, s, excluded)
							}
						}
						if noCompany && p.Company == "" {
							if k.ID != 0 && p.Requested {
								if err := c.catalog.MarkRequested(ctx, k.ID, c.now()); err != nil {
									c.log.Warn("collect: mark requested", "url", p.URL, "error", err)
								}
							}
							if excluded {
								cntExcluded++
							} else {
								cntRefused++
							}
							continue
						}
						_, outcome, upsertErr := c.catalog.Upsert(ctx, p, key)
						if upsertErr != nil {
							c.log.Warn("collect: upsert failed", "url", p.URL, "error", upsertErr)
							continue
						}
						if excluded {
							cntExcluded++
							continue
						}
						switch outcome {
						case catalog.OutcomeNew:
							cntNew++
						case catalog.OutcomeUpdated, catalog.OutcomeMerged:
							cntUpdated++
						case catalog.OutcomeUnchanged:
							cntUnchanged++
						case catalog.OutcomeTombstoned:
							cntRemoved++
						}
						if outcome != catalog.OutcomeTombstoned {
							c.mu.Lock()
							c.written++
							c.mu.Unlock()
						}
					}
				} else {
					c.log.Debug("collect: source failed", "key", key, "error", listErr)
				}

				runStatus, errText := sourceRunStatus(listErr, len(postings))
				c.writeSourceRun(j.ID, key, srcStarted, c.now(), runStatus, errText,
					len(postings), cntNew, cntUpdated, cntUnchanged, cntExcluded, cntRemoved, cntRefused)

				mu.Lock()
				doneCount++
				if listErr != nil {
					failedCount++
				}
				j.Progress(doneCount, len(enabled), "")
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if failedCount > 0 && failedCount == len(enabled) {
		return ErrEverySourceFailed
	}
	return nil
}

type everySourceFailed struct{}

func (everySourceFailed) Error() string { return "every source failed" }

func (everySourceFailed) Final() bool { return true }

func (everySourceFailed) Sentence() string { return "Every source failed" }

var ErrEverySourceFailed error = everySourceFailed{}

func (c *Collector) listSource(ctx context.Context, s config.SourceConfig) ([]listing.Posting, error) {
	u, _ := url.Parse(s.URL)
	mod, site := source.For(u)
	site.Name = s.Name
	return mod.List(ctx, c.fetcher, site)
}

func (c *Collector) addVacancyPage(ctx context.Context, p listing.Posting, s config.SourceConfig, excluded bool) (listing.Posting, bool) {
	pu, _ := url.Parse(p.URL)
	if pu == nil {
		pu = &url.URL{}
	}
	_, pageSite := source.For(pu)
	pageSite.Name = s.Name
	det, detErr := source.Detail(ctx, c.fetcher, p, pageSite)
	switch {
	case detErr == nil:
		p = det
		p.Requested = true
	case errors.Is(detErr, fetch.ErrBlocked):
		p.TextState = listing.TextBlocked
	case !errors.Is(detErr, fetch.ErrOffline) && !errors.Is(detErr, fetch.ErrRefused):
		p.Requested = true
	}
	if excl, exclErr := catalog.Excluded(ctx, c.db, p.Title, p.Company, p.ApplyURL); exclErr != nil {
		c.log.Warn("collect: exclusion check after detail", "error", exclErr)
	} else {
		excluded = excl
	}
	return p, excluded
}
