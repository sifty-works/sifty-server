package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/inbox"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/server"
)

func runPrepare(ctx context.Context, j *runner.Job, database *db.DB, reader formread.FormReader, now func() time.Time) error {
	vacancy, err := strconv.ParseInt(j.Subject, 10, 64)
	if err != nil {
		return runner.Permanent(err)
	}
	pages, err := formread.Addresses(ctx, database, vacancy)
	if errors.As(err, new(apperr.NotFound)) {
		return nil
	}
	if err != nil {
		return err
	}
	rel, err := j.Lane(ctx, "net")
	if err != nil {
		return err
	}
	defer rel()
	for _, page := range pages {
		form, err := reader.Read(ctx, page)
		var unreadable formread.Unreadable
		switch {
		case errors.Is(err, formread.ErrNoTemplate):
			continue
		case errors.As(err, &unreadable):
			return formread.Save(ctx, database, vacancy, formread.Form{URL: page, System: unreadable.System}, formread.StateUnreadable, unreadable.Reason, now())
		case err != nil:
			if errors.Is(err, fetch.ErrOffline) {
				return runner.Permanent(err)
			}
			return err
		}
		return formread.Save(ctx, database, vacancy, form, formread.StateRead, "", now())
	}
	return formread.Save(ctx, database, vacancy, formread.Form{}, formread.StateUnsupported, "", now())
}

func queuePrepare(ctx context.Context, database *db.DB, enqueue func(runner.Spec) (int64, error), subject string) error {
	vacancy, err := strconv.ParseInt(subject, 10, 64)
	if err != nil {
		return err
	}
	if stored, have, err := formread.Load(ctx, database, vacancy); err != nil || (have && stored.State != formread.StateUnsupported) {
		return err
	}
	_, err = enqueue(runner.Spec{Kind: formread.Kind, Subject: subject})
	return err
}

func registerApplicationRoutes(mux *http.ServeMux, logger *slog.Logger, database *db.DB, enqueue func(kind, subject string) (int64, error)) {
	vacancyOf := func(r *http.Request) (int64, error) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			return 0, apperr.NotFound{Message: "Role not found."}
		}
		return inbox.ResolveVacancy(r.Context(), database, id)
	}
	server.Handle(mux, "GET /api/vacancies/{id}/application", logger, func(r *http.Request, _ struct{}) (formread.ApplicationView, error) {
		vacancy, err := vacancyOf(r)
		if err != nil {
			return formread.ApplicationView{}, err
		}
		return formread.ViewOf(r.Context(), database, vacancy)
	})
	server.Handle(mux, "POST /api/vacancies/{id}/application/retry", logger, func(r *http.Request, _ struct{}) (formread.ApplicationView, error) {
		vacancy, err := vacancyOf(r)
		if err != nil {
			return formread.ApplicationView{}, err
		}
		if _, err := enqueue(formread.Kind, strconv.FormatInt(vacancy, 10)); err != nil {
			return formread.ApplicationView{}, err
		}
		return formread.ViewOf(r.Context(), database, vacancy)
	})
}
