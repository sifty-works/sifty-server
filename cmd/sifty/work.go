package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/collect"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/shortlistcheck"
)

const (
	failedWindow = 7 * 24 * time.Hour
	failedLimit  = 50
)

type jobKind struct {
	label   string
	work    string
	noun    string
	item    string
	subject func(ctx context.Context, database *db.DB, subject string) (string, error)
}

var jobKinds = map[string]jobKind{
	"collect":          {label: "Collection"},
	"recompute_fit":    {label: "Recomputing Fits you"},
	"retention":        {label: "Text retention"},
	"read_vacancy":     {label: "Reading vacancy text", work: "reading", noun: "reading", item: "vacancy text", subject: readSubject},
	"shortlist_check":  {label: "Shortlist check", work: "shortlist_check", noun: "the Shortlist check", item: "vacancy text", subject: shortlistSubject},
	"analyze_document": {label: "Document analysis", work: "reading", noun: "reading", item: "document", subject: documentSubject},
}

func documentSubject(ctx context.Context, database *db.DB, subject string) (string, error) {
	id, err := strconv.ParseInt(subject, 10, 64)
	if err != nil {
		return "", nil
	}
	return candidate.DocumentName(ctx, database, id)
}

func readSubject(ctx context.Context, database *db.DB, subject string) (string, error) {
	id, _, err := parseReadVacancySubject(subject)
	if err != nil {
		return "", nil
	}
	return vacancyName(ctx, database, id)
}

func shortlistSubject(ctx context.Context, database *db.DB, subject string) (string, error) {
	_, id, err := shortlistcheck.ParseSubject(subject)
	if err != nil {
		return "", nil
	}
	return vacancyName(ctx, database, id)
}

func vacancyName(ctx context.Context, database *db.DB, id int64) (string, error) {
	title, company, err := catalog.New(database, nil).TitleCompany(ctx, id)
	if err != nil || company == "" {
		return title, err
	}
	return title + " at " + company, nil
}

func failureSentence(kind string, err error) (string, bool) {
	k := jobKinds[kind]
	switch {
	case errors.Is(err, models.ErrNoProvider):
		return "No model is chosen for " + k.noun + ". Open Settings.", true
	case errors.Is(err, models.ErrOffline):
		return "Model calls are unavailable in offline mode. Open Settings.", true
	case errors.Is(err, models.ErrPromptTooLong):
		return "The " + k.item + " is too long for the chosen model. Choose a model with a longer context in Settings.", true
	case errors.Is(err, models.ErrBulkOnPaid):
		return "The provider is paid, so this work is refused. Allow paid bulk work in Settings.", true
	case errors.Is(err, models.ErrProviderUnavailable):
		return "The provider did not answer. Check it in Settings.", true
	case errors.Is(err, models.ErrUnusableAnswers):
		return "The provider kept giving answers that could not be used. Check it in Settings.", true
	case errors.Is(err, collect.ErrEverySourceFailed):
		return "Every source failed", true
	}
	return "", false
}

func waitText(until time.Time) string {
	return until.Local().Format("15:04, 2 Jan")
}

type workProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type workRunning struct {
	ID       int64         `json:"id"`
	Kind     string        `json:"kind"`
	Label    string        `json:"label"`
	Subject  string        `json:"subject"`
	Progress *workProgress `json:"progress"`
	Since    string        `json:"since"`
}

type workReason struct {
	Code     string `json:"code"`
	Text     string `json:"text"`
	Provider string `json:"provider,omitempty"`
	Until    string `json:"until,omitempty"`
}

type workWaiting struct {
	Kind   string      `json:"kind"`
	Label  string      `json:"label"`
	Count  int         `json:"count"`
	Reason *workReason `json:"reason"`
}

type workFailed struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Subject string `json:"subject"`
	At      string `json:"at"`
	Reason  string `json:"reason"`
}

type workResp struct {
	Running []workRunning `json:"running"`
	Waiting []workWaiting `json:"waiting"`
}

func label(kind string) string {
	if k, ok := jobKinds[kind]; ok {
		return k.label
	}
	return kind
}

func subjectOf(ctx context.Context, database *db.DB, j runner.Job) (string, error) {
	if k := jobKinds[j.Kind]; k.subject != nil {
		return k.subject(ctx, database, j.Subject)
	}
	return "", nil
}

func workHandler(database *db.DB, gate *models.Gate, r *runner.Runner, now func() time.Time) func(*http.Request, struct{}) (workResp, error) {
	return func(req *http.Request, _ struct{}) (workResp, error) {
		out := workResp{Running: []workRunning{}, Waiting: []workWaiting{}}
		if r == nil {
			return out, nil
		}
		ctx := req.Context()
		for _, j := range r.Running() {
			item := workRunning{ID: j.ID, Kind: j.Kind, Label: label(j.Kind), Since: j.Started.UTC().Format(time.RFC3339)}
			var err error
			if item.Subject, err = subjectOf(ctx, database, j); err != nil {
				return workResp{}, err
			}
			if j.Total > 0 {
				item.Progress = &workProgress{Done: j.Done, Total: j.Total}
			}
			out.Running = append(out.Running, item)
		}
		queued, err := r.Queued()
		if err != nil {
			return workResp{}, err
		}
		if out.Waiting, err = waitingWork(ctx, gate, queued, now()); err != nil {
			return workResp{}, err
		}
		return out, nil
	}
}

func failedWork(ctx context.Context, database *db.DB, r *runner.Runner, now func() time.Time) ([]workFailed, error) {
	out := []workFailed{}
	if r == nil {
		return out, nil
	}
	failed, err := r.Failed(now().Add(-failedWindow), failedLimit)
	if err != nil {
		return nil, err
	}
	for _, j := range failed {
		item := workFailed{ID: j.ID, Kind: j.Kind, Label: label(j.Kind), At: j.Updated.UTC().Format(time.RFC3339), Reason: j.Error}
		if item.Subject, err = subjectOf(ctx, database, j); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func waitingWork(ctx context.Context, gate *models.Gate, queued []runner.Job, now time.Time) ([]workWaiting, error) {
	var order []string
	byKind := map[string][]runner.Job{}
	for _, j := range queued {
		if _, seen := byKind[j.Kind]; !seen {
			order = append(order, j.Kind)
		}
		byKind[j.Kind] = append(byKind[j.Kind], j)
	}
	out := []workWaiting{}
	for _, kind := range order {
		jobs := byKind[kind]
		reason, err := waitReason(ctx, gate, jobKinds[kind].work, jobs, now)
		if err != nil {
			return nil, err
		}
		out = append(out, workWaiting{Kind: kind, Label: label(kind), Count: len(jobs), Reason: reason})
	}
	return out, nil
}

func waitReason(ctx context.Context, gate *models.Gate, work string, jobs []runner.Job, now time.Time) (*workReason, error) {
	if gate != nil && work != "" {
		cfg := gate.Store().Get()
		paused, err := gate.IsPaused(ctx)
		if err != nil {
			return nil, err
		}
		if paused {
			return &workReason{Code: "paused", Text: "Background model work is paused."}, nil
		}
		until, reached, err := gate.DailyLimitUntil(ctx)
		if err != nil {
			return nil, err
		}
		if reached {
			return &workReason{Code: "daily_limit", Text: "The daily limit of model calls is reached; work resumes at " + waitText(until) + ".", Until: until.UTC().Format(time.RFC3339)}, nil
		}
		provider := cfg.WorkProviderFor(work)
		if h, held := gate.Held(provider); held {
			why, _ := failureSentence("", h.Why)
			return &workReason{Code: "held", Text: why + " Trying again at " + waitText(h.Until) + ".", Provider: provider, Until: h.Until.UTC().Format(time.RFC3339)}, nil
		}
		if pc, ok := cfg.Providers[provider]; ok && pc.Cost == "paid" && !cfg.AllowPaidBulk {
			return &workReason{Code: "paid", Text: "Waiting for your permission to use a paid provider. Allow paid bulk work in Settings.", Provider: provider}, nil
		}
	}
	earliest := jobs[0].NotBefore
	for _, j := range jobs {
		if j.NotBefore.Before(earliest) {
			earliest = j.NotBefore
		}
	}
	if earliest.After(now) {
		return &workReason{Code: "retry", Text: "Waiting to try again at " + waitText(earliest) + ".", Until: earliest.UTC().Format(time.RFC3339)}, nil
	}
	return nil, nil
}
