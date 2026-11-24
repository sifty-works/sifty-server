package candidate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
)

type ProposalItem struct {
	ID           int64  `json:"id"`
	DocumentID   int64  `json:"documentId"`
	DocumentName string `json:"documentName"`
	Field        string `json:"field"`
	Value        string `json:"value"`
	Quote        string `json:"quote"`
	CreatedAt    string `json:"createdAt"`
	Current      string `json:"current,omitempty"`
}

type ProposalListResp struct {
	Proposals []ProposalItem `json:"proposals"`
}

func UpdateDocumentAnalysed(ctx context.Context, database *db.DB, docID int64, factCount int) error {
	return updateDocumentState(ctx, database, docID, "analysed", strconv.Itoa(factCount))
}

func UpdateDocumentError(ctx context.Context, database *db.DB, docID int64, reason string) error {
	return updateDocumentState(ctx, database, docID, "error", reason)
}

func SaveProposals(ctx context.Context, database *db.DB, docID int64, facts []RawFact) (int, error) {
	tx, err := beginTx(ctx, database)
	if err != nil {
		return 0, fmt.Errorf("candidate: begin save proposals: %w", err)
	}
	defer tx.Rollback()
	answeredRows, err := selectAnsweredProposalKeys(ctx, tx, docID)
	if err != nil {
		return 0, err
	}
	answered := map[string]bool{}
	for _, k := range answeredRows {
		answered[factKey(k.Field, k.Value)] = true
	}
	if err := deleteReviewProposals(ctx, tx, docID); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	saved := 0
	for _, f := range facts {
		if answered[factKey(f.Field, f.Value)] {
			continue
		}
		if err := insertProposal(ctx, tx, docID, f, now); err != nil {
			return 0, err
		}
		saved++
	}
	return saved, tx.Commit()
}

func CancelAnalysisHandler(database *db.DB, cancel func(kind, subject string) error) func(*http.Request, struct{}) (DocumentItem, error) {
	return func(r *http.Request, _ struct{}) (DocumentItem, error) {
		ctx := r.Context()
		id, err := parseDocumentID(r)
		if err != nil {
			return DocumentItem{}, err
		}
		row, err := loadDocumentRow(ctx, database, id)
		if err != nil {
			return DocumentItem{}, err
		}
		if row.State != "analysing" {
			return DocumentItem{}, apperr.Conflict{Message: "This document is not being analysed."}
		}
		if err := cancel("analyze_document", strconv.FormatInt(id, 10)); err != nil {
			return DocumentItem{}, fmt.Errorf("candidate: cancel analysis: %w", err)
		}
		if err := updateDocumentAnalysisCancelled(ctx, database, id); err != nil {
			return DocumentItem{}, err
		}
		row, err = loadDocumentRow(ctx, database, id)
		if err != nil {
			return DocumentItem{}, err
		}
		return rowToItem(row), nil
	}
}

func listProposals(ctx context.Context, database *db.DB) (ProposalListResp, error) {
	me, _, profile, err := loadHeldData(ctx, database)
	if err != nil {
		return ProposalListResp{}, err
	}
	rows, err := selectProposalsToReview(ctx, database)
	if err != nil {
		return ProposalListResp{}, err
	}
	items := []ProposalItem{}
	for _, it := range rows {
		kind, ok := fieldByName(it.Field)
		if !ok {
			continue
		}
		state, current := kind.check(it.Value, me, profile)
		if state == held {
			continue
		}
		it.Current = current
		items = append(items, it)
	}
	return ProposalListResp{Proposals: items}, nil
}

func loadHeldData(ctx context.Context, database *db.DB) (MeData, int64, ProfileData, error) {
	me, err := LoadMe(ctx, database)
	if err != nil {
		return MeData{}, 0, ProfileData{}, err
	}
	id, err := ActiveProfileID(ctx, database)
	if errors.As(err, &apperr.NotFound{}) {
		return me, 0, ProfileData{}, nil
	}
	if err != nil {
		return MeData{}, 0, ProfileData{}, err
	}
	profile, err := LoadProfileData(ctx, database, id)
	return me, id, profile, err
}

func parseProposalID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, apperr.NotFound{Message: "Suggestion not found."}
	}
	return id, nil
}

type waitingProposal struct {
	id    int64
	field string
	value string
}

func AcceptProposalHandler(database *db.DB, changed func()) func(*http.Request, struct{}) (ProposalListResp, error) {
	return func(r *http.Request, _ struct{}) (ProposalListResp, error) {
		ctx := r.Context()
		id, err := parseProposalID(r)
		if err != nil {
			return ProposalListResp{}, err
		}
		p, err := loadWaitingProposal(ctx, database, id)
		if err != nil {
			return ProposalListResp{}, err
		}
		kind, ok := fieldByName(p.field)
		if !ok {
			return ProposalListResp{}, apperr.Invalid{Message: "This kind of suggestion can no longer be accepted."}
		}
		me, profileID, profile, err := loadHeldData(ctx, database)
		if err != nil {
			return ProposalListResp{}, err
		}
		tx, err := beginTx(ctx, database)
		if err != nil {
			return ProposalListResp{}, fmt.Errorf("candidate: begin accept: %w", err)
		}
		defer tx.Rollback()
		if err := markAnswered(ctx, tx, id); err != nil {
			return ProposalListResp{}, err
		}
		wrote := false
		if state, _ := kind.check(p.value, me, profile); state != held {
			wrote, err = writeProposal(ctx, tx, kind, p, me, profileID, profile)
			if err != nil {
				return ProposalListResp{}, err
			}
		}
		if err := tx.Commit(); err != nil {
			return ProposalListResp{}, fmt.Errorf("candidate: commit accept: %w", err)
		}
		if wrote && changed != nil {
			changed()
		}
		return listProposals(ctx, database)
	}
}

func writeProposal(ctx context.Context, x execer, kind proposalField, p waitingProposal, me MeData, profileID int64, profile ProfileData) (bool, error) {
	newMe, newProfile := me, profile
	kind.apply(p.value, &newMe, &newProfile)
	meChanged, profileChanged := !jsonEqual(me, newMe), !jsonEqual(profile, newProfile)
	if meChanged {
		if errs := ValidateMeData(newMe); len(errs) > 0 {
			return false, apperr.Invalid{Message: strings.Join(errs, "; ")}
		}
		if err := SaveMe(ctx, x, newMe); err != nil {
			return false, err
		}
	}
	if profileChanged {
		if profileID == 0 {
			return false, apperr.NotFound{Message: "Profile not found."}
		}
		if err := saveProfileRow(ctx, x, profileID, newProfile); err != nil {
			return false, err
		}
	}
	return meChanged || profileChanged, nil
}

func RejectProposalHandler(database *db.DB) func(*http.Request, struct{}) (ProposalListResp, error) {
	return func(r *http.Request, _ struct{}) (ProposalListResp, error) {
		ctx := r.Context()
		id, err := parseProposalID(r)
		if err != nil {
			return ProposalListResp{}, err
		}
		if _, err := loadWaitingProposal(ctx, database, id); err != nil {
			return ProposalListResp{}, err
		}
		if err := markAnswered(ctx, database, id); err != nil {
			return ProposalListResp{}, err
		}
		return listProposals(ctx, database)
	}
}
