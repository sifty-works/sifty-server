package formread

import (
	"cmp"
	"context"
	"strconv"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/locate"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type ApplicationView struct {
	State  string  `json:"state"`
	Text   string  `json:"text"`
	System string  `json:"system"`
	Action string  `json:"action"`
	Fields []Field `json:"fields"`
}

func ViewOf(ctx context.Context, database *db.DB, vacancy int64) (ApplicationView, error) {
	stored, have, err := Load(ctx, database, vacancy)
	if err != nil {
		return ApplicationView{}, err
	}
	subject := strconv.FormatInt(vacancy, 10)
	job, failure, err := runner.Latest(ctx, database, Kind, subject)
	if err != nil {
		return ApplicationView{}, err
	}
	if search, _, err := runner.Latest(ctx, database, locate.Kind, subject); job == "" && (search == "queued" || search == "running") {
		job = search
	} else if err != nil {
		return ApplicationView{}, err
	}
	v := ApplicationView{System: stored.System, Fields: []Field{}}
	if stored.State == StateRead {
		v.Fields = stored.Fields
	}
	switch {
	case job == "queued" || job == "running":
		v.State, v.Text = "reading", "Reading the application form…"
	case job == "failed" || job == "interrupted":
		v.State, v.Text, v.Action = "failed", cmp.Or(failure, "Reading the form was interrupted."), "Retry preparation"
	case have && stored.State == StateRead:
		v.State = "ready"
	case have && stored.State == StateEmpty:
		v.State, v.Text, v.Action = "empty", "No questions to answer", "Apply directly"
	case have && stored.State == StateUnreadable:
		v.State, v.Text, v.Action = "unreadable", "The "+stored.System+" form could not be read", "Paste the form"
	default:
		v.State, v.Text, v.Action = "unsupported", "This site's form can't be read here", "Paste the form"
	}
	return v, nil
}
