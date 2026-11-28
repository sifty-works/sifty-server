package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/analysis"
	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func loadForAnalysis(database *db.DB) analysis.Loader {
	return func(ctx context.Context, id int64) (*analysis.Doc, int64, bool, error) {
		t, err := catalog.New(database, nil).VacancyText(ctx, id)
		if errors.As(err, new(apperr.NotFound)) || (err == nil && listing.TextState(t.TextState) != listing.TextFull) {
			return nil, 0, false, nil
		}
		if err != nil {
			return nil, 0, false, err
		}
		body, err := catalog.Body(t.Body)
		if err != nil {
			return nil, 0, false, fmt.Errorf("analysis: vacancy %d: %w", id, err)
		}
		doc := &analysis.Doc{URL: t.URL, ApplyURL: t.ApplyURL, Title: t.Title, Company: t.Company, Location: t.Location, Body: body, Original: listing.PlainText(body)}
		doc.FirstSeen, _ = time.Parse(time.RFC3339, t.FirstSeen)
		json.Unmarshal([]byte(t.TagsJSON), &doc.Tags)
		json.Unmarshal([]byte(t.FieldsJSON), &doc.Fields)
		return doc, t.Version, true, nil
	}
}

func visibleInAProfile(ctx context.Context, database *db.DB) (string, []any, error) {
	ids, err := fit.ProfileIDs(ctx, database)
	if err != nil {
		return "", nil, err
	}
	clauses := []string{"EXISTS (SELECT 1 FROM decision d2 WHERE d2.vacancy=v.id)"}
	var args []any
	var failed []error
	for _, id := range ids {
		p, err := candidate.ProfileSearch(ctx, database, id)
		if err != nil {
			failed = append(failed, fmt.Errorf("analysis: profile %d: %w", id, err))
			continue
		}
		f := candidate.Visibility(p)
		if f.SQL != "" && f.SQL != "1=0" {
			clauses = append(clauses, "("+f.SQL+")")
			args = append(args, f.Args...)
		}
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args, errors.Join(failed...)
}

func queueAnalysis(ctx context.Context, database *db.DB, r *runner.Runner) error {
	visible, args, profileErr := visibleInAProfile(ctx, database)
	if visible == "" {
		return profileErr
	}
	if err := analysis.Queue(ctx, database, catalog.NotExcluded+" AND "+visible, args, r); err != nil {
		return err
	}
	return profileErr
}

func analysisSubject(ctx context.Context, database *db.DB, subject string) (string, error) {
	id, _, err := analysis.ParseSubject(subject)
	if err != nil {
		return "", nil
	}
	return vacancyName(ctx, database, id)
}
