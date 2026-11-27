package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
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
	subject func(ctx context.Context, database *db.DB, subject string) (string, error)
}

var jobKinds = map[string]jobKind{
	"collect":          {label: "Collection"},
	"recompute_fit":    {label: "Recomputing Fit"},
	"retention":        {label: "Text retention"},
	"analyze_vacancy":  {label: "Analyzing vacancy", subject: analysisSubject},
	"shortlist_check":  {label: "Shortlist check", work: "shortlist_check", subject: shortlistSubject},
	"analyze_document": {label: "Document analysis", work: "reading", subject: documentSubject},
	"read_model":       {label: "Reading companies", work: "reading", subject: readSubject},
}

func documentSubject(ctx context.Context, database *db.DB, subject string) (string, error) {
	id, err := strconv.ParseInt(subject, 10, 64)
	if err != nil {
		return "", nil
	}
	return candidate.DocumentName(ctx, database, id)
}

func readSubject(_ context.Context, _ *db.DB, subject string) (string, error) {
	return subject, nil
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

func waitText(until time.Time) string {
	return until.Local().Format("15:04, 2 Jan")
}

type workProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type workModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type workRunning struct {
	ID       int64         `json:"id"`
	Kind     string        `json:"kind"`
	Label    string        `json:"label"`
	Subject  string        `json:"subject"`
	Progress *workProgress `json:"progress"`
	Since    string        `json:"since"`
	Model    *workModel    `json:"model,omitempty"`
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
			item.Model = runningModel(gate, j.Kind)
			out.Running = append(out.Running, item)
		}
		queued, err := r.QueuedByKind()
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

func waitingWork(ctx context.Context, gate *models.Gate, queued []runner.QueuedKind, now time.Time) ([]workWaiting, error) {
	out := []workWaiting{}
	for _, q := range queued {
		reason, err := waitReason(ctx, gate, jobKinds[q.Kind].work, q.NotBefore, now)
		if err != nil {
			return nil, err
		}
		out = append(out, workWaiting{Kind: q.Kind, Label: label(q.Kind), Count: q.Count, Reason: reason})
	}
	return out, nil
}

func runningModel(gate *models.Gate, kind string) *workModel {
	work := jobKinds[kind].work
	if gate == nil || work == "" {
		return nil
	}
	cfg := gate.Store().Get()
	provider := cfg.WorkProviderFor(work)
	pc, ok := cfg.Providers[provider]
	if !ok {
		return nil
	}
	return &workModel{Provider: provider, Model: pc.Model}
}

func waitReason(ctx context.Context, gate *models.Gate, work string, earliest, now time.Time) (*workReason, error) {
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
			var why string
			var explained runner.Explained
			if errors.As(h.Why, &explained) {
				why = explained.Sentence()
			}
			return &workReason{Code: "held", Text: why + " Trying again at " + waitText(h.Until) + ".", Provider: provider, Until: h.Until.UTC().Format(time.RFC3339)}, nil
		}
		if pc, ok := cfg.Providers[provider]; ok && pc.Cost == "paid" && !cfg.AllowPaidBulk {
			return &workReason{Code: "paid", Text: "Waiting for your permission to use a paid provider. Allow paid bulk work in Settings.", Provider: provider}, nil
		}
	}
	if earliest.After(now) {
		return &workReason{Code: "retry", Text: "Waiting to try again at " + waitText(earliest) + ".", Until: earliest.UTC().Format(time.RFC3339)}, nil
	}
	return nil, nil
}
