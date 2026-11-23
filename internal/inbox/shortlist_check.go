package inbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/shortlistcheck"
)

type CheckAnswer struct {
	Index       int    `json:"index"`
	Requirement string `json:"requirement"`
	Verdict     string `json:"verdict"`
	Reason      string `json:"reason"`
	Excerpt     string `json:"excerpt"`
}

type CheckView struct {
	Answers  []CheckAnswer `json:"answers"`
	Made     string        `json:"made,omitempty"`
	Outdated bool          `json:"outdated"`
	Note     string        `json:"note,omitempty"`
	State    string        `json:"state,omitempty"`
	Active   bool          `json:"active"`
	Retry    bool          `json:"retry"`
}

type CheckRetryReq struct {
	Profile int64 `json:"profile"`
}

type checkState struct {
	stored   shortlistcheck.Stored
	have     bool
	current  bool
	shown    bool
	jobID    int64
	jobState string
	jobError string
	refused  error
}

func loadCheck(ctx context.Context, database *db.DB, enqueue func(kind, subject string) (int64, error),
	profileID, vacancyID int64, stage string, force bool) (checkState, error) {

	var cs checkState
	if stage == string(StageInbox) {
		return cs, nil
	}
	digest, err := shortlistcheck.Digest(ctx, database, profileID, vacancyID)
	if err != nil {
		return cs, err
	}
	if cs.stored, cs.have, err = shortlistcheck.Load(ctx, database, profileID, vacancyID); err != nil {
		return cs, err
	}
	cs.current = cs.have && cs.stored.Digest == digest
	subject := shortlistcheck.Subject(profileID, vacancyID)
	err = database.QueryRowContext(ctx,
		`SELECT id, state, error FROM job WHERE kind=? AND subject=? ORDER BY id DESC LIMIT 1`,
		shortlistcheck.Kind, subject).Scan(&cs.jobID, &cs.jobState, &cs.jobError)
	if err != nil && err != sql.ErrNoRows {
		return cs, fmt.Errorf("inbox: check job: %w", err)
	}
	active := cs.jobState == "queued" || cs.jobState == "running"
	failed := cs.jobState == "failed" || cs.jobState == "interrupted"
	owed := stage == string(StageShortlist) || cs.have
	cs.shown = cs.have || (owed && !cs.current)
	if cs.current || !owed || enqueue == nil || active || (failed && !force) {
		return cs, nil
	}
	id, err := enqueue(shortlistcheck.Kind, subject)
	switch {
	case errors.Is(err, models.ErrNoProvider), errors.Is(err, models.ErrOffline):
		cs.refused = err
	case err != nil:
		return cs, err
	default:
		cs.jobID, cs.jobState, cs.jobError = id, "queued", ""
	}
	return cs, nil
}

func (cs checkState) view(lines []fit.RequirementLine, queue []int64, now time.Time) *CheckView {
	if !cs.shown {
		return nil
	}
	v := &CheckView{Answers: []CheckAnswer{}}
	var open []fit.RequirementLine
	for _, l := range lines {
		if l.Verdict == "?" {
			open = append(open, l)
		}
	}
	for _, a := range cs.stored.Check.Answers {
		if l, ok := shortlistcheck.FindLine(open, a.Requirement); ok {
			v.Answers = append(v.Answers, CheckAnswer{l.Index, l.Text, a.Verdict, a.Reason, a.Excerpt})
		}
	}
	if cs.have {
		v.Made = cs.stored.Made.In(now.Location()).Format("2 Jan 2006")
	}
	if cs.current {
		return v
	}
	v.Active = cs.jobState == "queued" || cs.jobState == "running"
	v.Outdated = cs.have
	if v.Outdated {
		v.Note = "Based on your data of " + v.Made
	}
	switch {
	case cs.refused != nil && errors.Is(cs.refused, models.ErrOffline):
		v.State = "Model calls are unavailable in offline mode. Open Settings."
	case cs.refused != nil:
		v.State = "No model is chosen for the Shortlist check. Open Settings."
	case cs.jobState == "running":
		v.State = "Checking…"
	case cs.jobState == "queued":
		v.State = fmt.Sprintf("Waiting for the model: %d ahead", max(slices.Index(queue, cs.jobID), 0))
	case cs.jobState == "failed" || cs.jobState == "interrupted":
		reason := cs.jobError
		if reason == "" {
			reason = cs.jobState
		}
		v.State, v.Retry = "Check failed: "+reason, true
	}
	return v
}

func retryCheck(ctx context.Context, database *db.DB, enqueue func(kind, subject string) (int64, error), profileID, vacancyID int64) error {
	var stage string
	err := database.QueryRowContext(ctx, `SELECT stage FROM decision WHERE profile=? AND vacancy=?`, profileID, vacancyID).Scan(&stage)
	if err == sql.ErrNoRows {
		return apperr.Conflict{Message: "The role is not shortlisted."}
	}
	if err != nil {
		return fmt.Errorf("inbox: check retry: %w", err)
	}
	_, err = loadCheck(ctx, database, enqueue, profileID, vacancyID, stage, true)
	return err
}
