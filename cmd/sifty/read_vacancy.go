package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/reading"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func runReadVacancy(
	ctx context.Context,
	j *runner.Job,
	database *db.DB,
	model func(vacancyID int64) models.Model,
) error {
	vacancyID, version, err := parseReadVacancySubject(j.Subject)
	if err != nil {
		return fmt.Errorf("read_vacancy: bad subject %q: %w", j.Subject, err)
	}

	cat := catalog.New(database, nil)
	v, have, currentVersion, ok, err := fit.LoadVacancy(ctx, cat, vacancyID)
	if err != nil || !ok || currentVersion != version {
		return err
	}

	input := reading.ReadInput{Title: v.Title, Company: v.Company, Text: v.Text, Have: have}

	rec, readerVer, err := reading.Read(ctx, model(vacancyID), input)
	if err != nil {
		return err
	}

	if err := catalog.SaveReading(ctx, database, vacancyID, version, readerVer, rec); err != nil {
		if catalog.IsFKError(err) {
			return nil
		}
		return err
	}
	return nil
}

func parseReadVacancySubject(s string) (int64, int64, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected <id>/<version>")
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad vacancy id: %w", err)
	}
	ver, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad version: %w", err)
	}
	return id, ver, nil
}
