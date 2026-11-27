package main

import (
	"context"

	"github.com/sifty-works/sifty-server/internal/analysis"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

const undecidedAndNotBroken = `NOT EXISTS (SELECT 1 FROM decision dr WHERE dr.vacancy=v.id)
	AND NOT (EXISTS (SELECT 1 FROM fit fb WHERE fb.vacancy=v.id AND fb.broken=1) AND NOT EXISTS (SELECT 1 FROM fit fo WHERE fo.vacancy=v.id AND fo.broken=0))`

type reading struct {
	database *db.DB
	store    *config.Store
	runner   *runner.Runner
	ready    func(kind string) error
}

func (rd *reading) usable() bool {
	if rd == nil || rd.runner == nil {
		return false
	}
	work := models.NamedTask("read_domain").Work
	return rd.ready == nil || rd.ready(work) == nil
}

func (rd *reading) queue(ctx context.Context) error {
	if !rd.usable() {
		return nil
	}
	cfg := rd.store.Get()
	if !cfg.PreciseReading {
		return nil
	}
	if pc, ok := cfg.Providers[cfg.WorkProviderFor(models.NamedTask("read_domain").Work)]; ok && pc.Cost == "paid" && !cfg.AllowPaidBulk {
		return nil
	}
	visible, args, profileErr := visibleInAProfile(ctx, rd.database)
	if visible == "" {
		return profileErr
	}
	if err := analysis.QueueReading(ctx, rd.database, catalog.NotExcluded+" AND "+visible+" AND "+undecidedAndNotBroken, args, rd.runner); err != nil {
		return err
	}
	return profileErr
}

func (rd *reading) opened(ctx context.Context, vacancy int64) error {
	if !rd.usable() {
		return nil
	}
	subject, needed, err := analysis.ReadSubjectOf(ctx, rd.database, vacancy)
	if err != nil || !needed {
		return err
	}
	for _, j := range rd.runner.Active(analysis.ReadKind) {
		if j.Subject != subject {
			continue
		}
		if j.State != "queued" || j.Priority >= analysis.OpenedPriority {
			return nil
		}
		if err := rd.runner.Cancel(j.ID); err != nil {
			return err
		}
	}
	_, err = rd.runner.Enqueue(runner.Spec{Kind: analysis.ReadKind, Subject: subject, Priority: analysis.OpenedPriority})
	return err
}
