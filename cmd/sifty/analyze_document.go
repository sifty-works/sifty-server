package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func saveAnalyzeCheckpoint(ctx context.Context, j *runner.Job, cp analyzeCheckpoint) error {
	b, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("analyze_document: marshal checkpoint: %w", err)
	}
	return j.SaveParams(ctx, json.RawMessage(b))
}

const analyzeCallDeadline = 2 * time.Minute

type analyzeCheckpoint struct {
	NextChunk int                 `json:"nextChunk"`
	Facts     []candidate.RawFact `json:"facts"`
}

func runAnalyzeDocument(
	ctx context.Context,
	j *runner.Job,
	database *db.DB,
	model models.Model,
) error {
	docID, err := strconv.ParseInt(j.Subject, 10, 64)
	if err != nil {
		return fmt.Errorf("analyze_document: bad subject %q: %w", j.Subject, err)
	}

	text, exists, err := candidate.DocumentText(ctx, database, docID)
	if err != nil {
		return err
	}
	if !exists || strings.TrimSpace(text) == "" {
		return nil
	}

	me, err := candidate.LoadMe(ctx, database)
	if err != nil {
		return err
	}
	profileID, aerr := candidate.ActiveProfileID(ctx, database)
	var profile candidate.ProfileData
	if aerr == nil && profileID > 0 {
		profile, err = candidate.LoadProfileData(ctx, database, profileID)
		if err != nil {
			return err
		}
	}

	var cp analyzeCheckpoint
	if len(j.Params) > 0 {
		if err := json.Unmarshal(j.Params, &cp); err != nil {
			return fmt.Errorf("analyze_document: bad params: %w", err)
		}
	}

	chunks := candidate.SplitChunks(text)
	j.Progress(cp.NextChunk, len(chunks), "")

	for i, chunk := range chunks {
		if i < cp.NextChunk {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		callCtx, cancel := context.WithTimeout(ctx, analyzeCallDeadline)
		facts, err := candidate.ProposeFromChunk(callCtx, model, chunk)
		cancel()
		if err != nil {
			if j.Waits(err) {
				return err
			}
			reason := "The analysis could not be completed. Try again."
			var explained runner.Explained
			if errors.As(err, &explained) {
				reason = explained.Sentence()
			}
			if uerr := candidate.UpdateDocumentError(ctx, database, docID, reason); uerr != nil {
				return uerr
			}
			if serr := saveAnalyzeCheckpoint(ctx, j, cp); serr != nil {
				return serr
			}
			return err
		}
		cp.Facts = append(cp.Facts, facts...)
		cp.NextChunk = i + 1
		if serr := saveAnalyzeCheckpoint(ctx, j, cp); serr != nil {
			return serr
		}
		j.Progress(cp.NextChunk, len(chunks), "")
	}

	filtered := candidate.FilterFacts(text, cp.Facts, me, profile)

	saved, err := candidate.SaveProposals(ctx, database, docID, filtered)
	if err != nil {
		if catalog.IsFKError(err) {
			return nil
		}
		return err
	}
	return candidate.UpdateDocumentAnalysed(ctx, database, docID, saved)
}
