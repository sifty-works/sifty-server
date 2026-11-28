package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/collect"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/inbox"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/server"
)

func enqueueRecomputeFit(database *db.DB, r *runner.Runner, log *slog.Logger, rd *reading) func() {
	return func() {
		if err := queueAnalysis(context.Background(), database, r); err != nil {
			log.Warn("analysis not queued", "err", err)
		}
		if err := rd.queue(context.Background()); err != nil {
			log.Warn("reading not queued", "err", err)
		}
		r.Enqueue(runner.Spec{Kind: "recompute_fit"})
	}
}

func registerAllRoutes(
	mux *http.ServeMux,
	logger *slog.Logger,
	gate *models.Gate,
	database *db.DB,
	now func() time.Time,
	collectActive func() bool,
	sourceNames func() map[string]string,
	rules func() fit.Rules,
	recomputeFit func(),
	filesDir string,
	readers candidate.Readers,
	col *collect.Collector,
	r *runner.Runner,
	modelReady func(kind string) error,
	logPath string,
	heldKeys func() []string,
	rd *reading,
) {
	enqueue := func(kind, subject string) (int64, error) {
		if r == nil {
			return 0, nil
		}
		if id, ok, err := r.AdoptInterrupted(kind, subject); err != nil {
			return 0, err
		} else if ok {
			return id, nil
		}
		return r.Enqueue(runner.Spec{Kind: kind, Subject: subject})
	}
	cancel := func(kind, subject string) error {
		if r == nil {
			return nil
		}
		for _, j := range r.Active(kind) {
			if j.Subject == subject {
				return r.Cancel(j.ID)
			}
		}
		return nil
	}
	enqueueModelWork := func(kind, subject string) (int64, error) {
		if modelReady != nil {
			if err := modelReady(kind); err != nil {
				return 0, err
			}
		}
		return enqueue(kind, subject)
	}
	server.Handle(mux, "POST /api/providers/{name}/check", logger, models.CheckProviderHandler(gate))
	server.Handle(mux, "GET /api/work", logger, workHandler(database, gate, r, now))
	server.Handle(mux, "GET /api/logs", logger, logsHandler(database, r, now, logPath, heldKeys))
	server.Handle(mux, "GET /api/models", logger, models.ModelsHandler(gate))
	server.Handle(mux, "PATCH /api/models", logger, models.PatchModelsHandler(gate, recomputeFit))
	server.Handle(mux, "GET /api/secrets", logger, models.SecretsHandler(gate))
	server.Handle(mux, "PUT /api/secrets/{name}", logger, models.PutSecretHandler(gate))
	server.Handle(mux, "DELETE /api/secrets/{name}", logger, models.DeleteSecretHandler(gate))
	inbox.RegisterRoutes(mux, database, now, logger, collectActive, sourceNames, rules, recomputeFit, enqueueModelWork, rd.opened)
	candidate.RegisterRoutes(mux, database, filesDir, readers, logger, recomputeFit, enqueue)
	server.Handle(mux, "POST /api/documents/{id}/cancel", logger, candidate.CancelAnalysisHandler(database, cancel))
	col.RegisterRoutes(mux)
}
