package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
	"github.com/sifty-works/sifty-server/internal/shortlistcheck"
)

func runShortlistCheck(
	ctx context.Context,
	j *runner.Job,
	database *db.DB,
	model func(vacancyID int64) models.Model,
	now func() time.Time,
) error {
	profileID, vacancyID, err := shortlistcheck.ParseSubject(j.Subject)
	if err != nil {
		return runner.Permanent(err)
	}
	digest, err := shortlistcheck.Digest(ctx, database, profileID, vacancyID)
	var missing apperr.NotFound
	if errors.Is(err, sql.ErrNoRows) || errors.As(err, &missing) {
		return nil
	}
	if err != nil {
		return err
	}

	v, merged, _, ok, err := fit.LoadVacancy(ctx, catalog.New(database, nil), vacancyID)
	if err != nil || !ok {
		return err
	}
	prof, err := fit.LoadProfile(ctx, database, profileID, fit.Rules{})
	if err != nil {
		return err
	}
	me, err := candidate.LoadMe(ctx, database)
	if err != nil {
		return err
	}
	pd, err := candidate.EffectiveProfile(ctx, database, profileID)
	if err != nil {
		return err
	}
	docs, err := candidate.EvidenceDocuments(ctx, database)
	if err != nil {
		return err
	}

	var checker shortlistcheck.ShortlistChecker = shortlistcheck.Checker{Model: model(vacancyID), Text: v.Text}
	check, err := checker.Check(ctx, prof.View, candidate.EvidenceText(me, pd, docs), merged)
	if err != nil {
		return fmt.Errorf("shortlist check: %w", err)
	}
	if err := shortlistcheck.Save(ctx, database, profileID, vacancyID, digest, check, now()); err != nil && !catalog.IsFKError(err) {
		return err
	}
	return nil
}
