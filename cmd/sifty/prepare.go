package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/answer"
	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/inbox"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/models"
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

type application struct {
	view    formread.ApplicationView
	form    []formread.Field
	known   answer.Known
	company string
}

func loadApplication(ctx context.Context, database *db.DB, vacancy int64) (application, error) {
	v, err := formread.ViewOf(ctx, database, vacancy)
	a := application{view: v, form: v.Fields}
	if err != nil || len(v.Fields) == 0 {
		return a, err
	}
	if a.known, err = (answerData{database}).Known(ctx); err != nil {
		return application{}, err
	}
	subject, err := formread.SubjectOf(ctx, database, vacancy)
	if err != nil {
		return application{}, err
	}
	a.company = subject.Company
	a.view.Fields = slices.DeleteFunc(slices.Clone(v.Fields), func(f formread.Field) bool { return !answer.Open(f, a.known, a.company) })
	return a, nil
}

func applicationView(ctx context.Context, database *db.DB, vacancy int64) (formread.ApplicationView, error) {
	a, err := loadApplication(ctx, database, vacancy)
	return a.view, err
}

func requirePrepared(fields []formread.Field) error {
	if len(fields) == 0 {
		return apperr.NotFound{Message: "This role has no application form yet."}
	}
	return nil
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

type chatPost struct {
	Message string   `json:"message"`
	Field   string   `json:"field"`
	Values  []string `json:"values"`
}

type chatView struct {
	Messages []answer.ChatMessage `json:"messages"`
}

const chatFailure = "The model could not answer. Try again."

func converse(ctx context.Context, database *db.DB, source modelSource, now func() time.Time, logger *slog.Logger, vacancy int64, body chatPost) (chatView, error) {
	a, err := loadApplication(ctx, database, vacancy)
	if err == nil {
		err = requirePrepared(a.form)
	}
	if err == nil && body.Field != "" && !slices.ContainsFunc(a.form, func(f formread.Field) bool { return f.ID == body.Field }) {
		err = apperr.NotFound{Message: "This question is not in the form."}
	}
	text := strings.TrimSpace(body.Message)
	if err == nil && text == "" {
		err = apperr.Invalid{Message: "The message is empty."}
	}
	if err != nil {
		return chatView{}, err
	}
	subject, err := vacancyForAnswers(ctx, database, vacancy)
	if err != nil {
		return chatView{}, err
	}
	history, err := answer.Messages(ctx, database, vacancy)
	if err != nil {
		return chatView{}, err
	}
	chatter := answer.Chatter{Model: source.For(models.NamedTask("rewrite_answer").Work, "user", vacancy)}
	reply, err := chatter.Reply(ctx, subject, a.known, a.view.Fields, history, answer.Turn{Text: text, Field: body.Field, Values: body.Values})
	if err != nil {
		var explained runner.Explained
		if errors.As(err, &explained) {
			return chatView{}, apperr.Invalid{Message: explained.Sentence()}
		}
		logger.Warn("answer chat failed", "vacancy", vacancy, "err", err)
		return chatView{}, apperr.Invalid{Message: chatFailure}
	}
	asked := answer.ChatMessage{Role: answer.RoleUser, Text: text, Field: body.Field}
	if err := answer.AddExchange(ctx, database, vacancy, now(), asked, reply); err != nil {
		return chatView{}, err
	}
	return conversation(ctx, database, vacancy)
}

func conversation(ctx context.Context, database *db.DB, vacancy int64) (chatView, error) {
	messages, err := answer.Messages(ctx, database, vacancy)
	return chatView{Messages: messages}, err
}

func registerApplicationRoutes(mux *http.ServeMux, logger *slog.Logger, database *db.DB, enqueue func(kind, subject string) (int64, error), source modelSource, now func() time.Time) {
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
		a, err := loadApplication(r.Context(), database, vacancy)
		if err != nil {
			return formread.ApplicationView{}, err
		}
		if i := slices.IndexFunc(a.view.Fields, func(f formread.Field) bool { return f.ID == r.PathValue("field") }); i >= 0 {
			if err := candidate.SaveQA(r.Context(), database, a.view.Fields[i].Label, strings.Join(body.Values, ", "), a.company, now()); err != nil {
				return formread.ApplicationView{}, err
			}
		}
		return a.view, nil
	})
	server.Handle(mux, "GET /api/vacancies/{id}/application/chat", logger, func(r *http.Request, _ struct{}) (chatView, error) {
		vacancy, err := vacancyOf(r)
		if err != nil {
			return chatView{}, err
		}
		view, err := formread.ViewOf(r.Context(), database, vacancy)
		if err == nil {
			err = requirePrepared(view.Fields)
		}
		if err != nil {
			return chatView{}, err
		}
		return conversation(r.Context(), database, vacancy)
	})
	server.Handle(mux, "POST /api/vacancies/{id}/application/chat", logger, func(r *http.Request, body chatPost) (chatView, error) {
		vacancy, err := vacancyOf(r)
		if err != nil {
			return chatView{}, err
		}
		return converse(r.Context(), database, source, now, logger, vacancy, body)
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
