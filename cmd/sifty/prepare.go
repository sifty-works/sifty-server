package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/sifty-works/sifty-server/internal/answer"
	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/inbox"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/server"
)

func runPrepare(ctx context.Context, j *runner.Job, database *db.DB, reader formread.FormReader, answerer func(vacancy int64) answer.Answerer, now func() time.Time) error {
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
		if err := formread.Save(ctx, database, vacancy, form, formread.StateRead, "", now()); err != nil {
			return err
		}
		stored, _, err := formread.Load(ctx, database, vacancy)
		if err != nil {
			return err
		}
		subject, err := vacancyForAnswers(ctx, database, vacancy)
		if err != nil {
			return err
		}
		answered, _, err := answer.Apply(ctx, database, answerer(vacancy), subject, vacancy, stored.Form)
		if err == nil {
			j.Progress(answered, len(stored.Fields), fmt.Sprintf("Answered %d of %d field(s)", answered, len(stored.Fields)))
		}
		return err
	}
	return formread.Save(ctx, database, vacancy, formread.Form{}, formread.StateUnsupported, "", now())
}

func vacancyForAnswers(ctx context.Context, database *db.DB, id int64) (answer.Vacancy, error) {
	t, err := catalog.New(database, nil).VacancyText(ctx, id)
	if err != nil {
		return answer.Vacancy{}, err
	}
	html, err := catalog.Body(t.Body)
	if err != nil {
		return answer.Vacancy{}, err
	}
	return answer.Vacancy{Title: t.Title, Company: t.Company, Text: listing.PlainText(html)}, nil
}

func applicationView(ctx context.Context, database *db.DB, vacancy int64) (formread.ApplicationView, error) {
	v, err := formread.ViewOf(ctx, database, vacancy)
	if err != nil || len(v.Fields) == 0 {
		return v, err
	}
	known, err := answerData{database}.Known(ctx)
	if err != nil {
		return formread.ApplicationView{}, err
	}
	v.Fields = slices.DeleteFunc(v.Fields, func(f formread.Field) bool { return !answer.Open(f, known) })
	return v, nil
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

type fieldEdit struct {
	Values   []string `json:"values"`
	Revision int64    `json:"revision"`
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
		return applicationView(r.Context(), database, vacancy)
	})
	server.Handle(mux, "PATCH /api/vacancies/{id}/application/fields/{field}", logger, func(r *http.Request, body fieldEdit) (formread.ApplicationView, error) {
		vacancy, err := vacancyOf(r)
		if err == nil {
			err = formread.Edit(r.Context(), database, vacancy, r.PathValue("field"), body.Values, body.Revision)
		}
		if err != nil {
			return formread.ApplicationView{}, err
		}
		return applicationView(r.Context(), database, vacancy)
	})
	server.Handle(mux, "POST /api/vacancies/{id}/application/retry", logger, func(r *http.Request, _ struct{}) (formread.ApplicationView, error) {
		vacancy, err := vacancyOf(r)
		if err != nil {
			return formread.ApplicationView{}, err
		}
		if _, err := enqueue(formread.Kind, strconv.FormatInt(vacancy, 10)); err != nil {
			return formread.ApplicationView{}, err
		}
		return applicationView(r.Context(), database, vacancy)
	})
}
