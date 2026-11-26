package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sifty-works/sifty-server/internal/analysis"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func loadForAnalysis(database *db.DB) analysis.Loader {
	return func(ctx context.Context, id int64) (*analysis.Doc, int64, bool, error) {
		batch, err := catalog.New(database, nil).VacancyTexts(ctx, id-1, 1)
		if err != nil || len(batch) == 0 || batch[0].ID != id || listing.TextState(batch[0].TextState) != listing.TextFull {
			return nil, 0, false, err
		}
		t := batch[0]
		body, err := catalog.Body(t.Body)
		if err != nil {
			return nil, 0, false, fmt.Errorf("analysis: vacancy %d: %w", id, err)
		}
		doc := &analysis.Doc{URL: t.URL, ApplyURL: t.ApplyURL, Title: t.Title, Company: t.Company, Location: t.Location, Body: body, Original: listing.PlainText(body)}
		json.Unmarshal([]byte(t.TagsJSON), &doc.Tags)
		json.Unmarshal([]byte(t.FieldsJSON), &doc.Fields)
		return doc, t.Version, true, nil
	}
}

func queueAnalysis(ctx context.Context, database *db.DB, r *runner.Runner) error {
	visible, args, profileErr := readingVisibility(ctx, database)
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
