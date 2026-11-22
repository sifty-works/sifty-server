package candidate

import (
	"context"
	"database/sql"
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

func SaveProposals(ctx context.Context, database *db.DB, docID int64, facts []RawFact) (int, error) {
	return saveProposals(ctx, database, docID, facts)
}

func UpdateDocumentAnalysed(ctx context.Context, database *db.DB, docID int64, factCount int) error {
	return updateDocumentAnalysed(ctx, database, docID, factCount)
}

func UpdateDocumentError(ctx context.Context, database *db.DB, docID int64, reason string) error {
	return updateDocumentError(ctx, database, docID, reason)
}

func saveProposals(ctx context.Context, database *db.DB, docID int64, facts []RawFact) (int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("candidate: begin save proposals: %w", err)
	}
	defer tx.Rollback()
	answered, err := answeredKeys(ctx, tx, docID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM proposal WHERE document_id=? AND to_review=1`, docID); err != nil {
		return 0, fmt.Errorf("candidate: clear proposals: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	saved := 0
	for _, f := range facts {
		if answered[factKey(f.Field, f.Value)] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO proposal (document_id, field, value, quote, to_review, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
			docID, f.Field, f.Value, f.Quote, now); err != nil {
			return 0, fmt.Errorf("candidate: insert proposal: %w", err)
		}
		saved++
	}
	return saved, tx.Commit()
}

func answeredKeys(ctx context.Context, tx *sql.Tx, docID int64) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT field, value FROM proposal WHERE document_id=? AND to_review=0`, docID)
	if err != nil {
		return nil, fmt.Errorf("candidate: answered proposals: %w", err)
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var field, value string
		if err := rows.Scan(&field, &value); err != nil {
			return nil, fmt.Errorf("candidate: scan answered proposal: %w", err)
		}
		keys[factKey(field, value)] = true
	}
	return keys, rows.Err()
}

func updateDocumentAnalysed(ctx context.Context, database *db.DB, docID int64, factCount int) error {
	reason := fmt.Sprintf("%d", factCount)
	_, err := database.ExecContext(ctx,
		`UPDATE document SET state='analysed', state_reason=? WHERE id=?`, reason, docID)
	return err
}

func updateDocumentAnalysing(ctx context.Context, database *db.DB, docID int64) error {
	_, err := database.ExecContext(ctx,
		`UPDATE document SET state='analysing', state_reason='' WHERE id=?`, docID)
	return err
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
		if _, err := database.ExecContext(ctx,
			`UPDATE document SET state='ready', state_reason='Analysis cancelled.' WHERE id=? AND state='analysing'`, id); err != nil {
			return DocumentItem{}, fmt.Errorf("candidate: set cancelled: %w", err)
		}
		row, err = loadDocumentRow(ctx, database, id)
		if err != nil {
			return DocumentItem{}, err
		}
		return rowToItem(row), nil
	}
}

func ResetInterruptedAnalyses(ctx context.Context, database *db.DB) (int64, error) {
	res, err := database.ExecContext(ctx,
		`UPDATE document SET state='ready', state_reason='Analysis was interrupted. Press Analyze to continue.'
		 WHERE state='analysing'
		   AND NOT EXISTS (
		     SELECT 1 FROM job
		     WHERE kind='analyze_document' AND subject=CAST(document.id AS TEXT) AND state='queued')`)
	if err != nil {
		return 0, fmt.Errorf("candidate: reset interrupted analyses: %w", err)
	}
	return res.RowsAffected()
}

func updateDocumentError(ctx context.Context, database *db.DB, docID int64, reason string) error {
	_, err := database.ExecContext(ctx,
		`UPDATE document SET state='error', state_reason=? WHERE id=?`, reason, docID)
	return err
}

func listProposals(ctx context.Context, database *db.DB) (ProposalListResp, error) {
	me, _, profile, err := loadHeldData(ctx, database)
	if err != nil {
		return ProposalListResp{}, err
	}
	rows, err := database.QueryContext(ctx, `
		SELECT p.id, p.document_id, COALESCE(d.name,'(file deleted)'), p.field, p.value, p.quote, p.created_at
		FROM proposal p
		LEFT JOIN document d ON d.id = p.document_id
		WHERE p.to_review = 1
		ORDER BY p.id DESC`)
	if err != nil {
		return ProposalListResp{}, fmt.Errorf("candidate: list proposals: %w", err)
	}
	defer rows.Close()
	items := []ProposalItem{}
	for rows.Next() {
		var it ProposalItem
		if err := rows.Scan(&it.ID, &it.DocumentID, &it.DocumentName, &it.Field, &it.Value, &it.Quote, &it.CreatedAt); err != nil {
			return ProposalListResp{}, fmt.Errorf("candidate: scan proposal: %w", err)
		}
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
	if err := rows.Err(); err != nil {
		return ProposalListResp{}, fmt.Errorf("candidate: list proposals rows: %w", err)
	}
	return ProposalListResp{Proposals: items}, nil
}

func loadHeldData(ctx context.Context, database *db.DB) (MeData, int64, ProfileData, error) {
	me, err := LoadMe(ctx, database)
	if err != nil {
		return MeData{}, 0, ProfileData{}, err
	}
	id, err := activeProfileID(ctx, database)
	if errors.As(err, &apperr.NotFound{}) {
		return me, 0, ProfileData{}, nil
	}
	if err != nil {
		return MeData{}, 0, ProfileData{}, err
	}
	profile, err := loadProfileRow(ctx, database, id)
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

func loadWaitingProposal(ctx context.Context, database *db.DB, id int64) (waitingProposal, error) {
	p := waitingProposal{id: id}
	var toReview int
	err := database.QueryRowContext(ctx, `
		SELECT field, value, to_review FROM proposal WHERE id=?`, id).Scan(&p.field, &p.value, &toReview)
	if err == sql.ErrNoRows {
		return p, apperr.NotFound{Message: "Suggestion not found."}
	}
	if err != nil {
		return p, fmt.Errorf("candidate: load proposal: %w", err)
	}
	if toReview == 0 {
		return p, apperr.Conflict{Message: "This suggestion was already answered."}
	}
	return p, nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func markAnswered(ctx context.Context, x execer, id int64) error {
	res, err := x.ExecContext(ctx, `UPDATE proposal SET to_review=0 WHERE id=? AND to_review=1`, id)
	if err != nil {
		return fmt.Errorf("candidate: answer proposal: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return apperr.Conflict{Message: "This suggestion was already answered."}
	}
	return nil
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
		tx, err := database.BeginTx(ctx, nil)
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
		if err := saveMeData(ctx, x, newMe); err != nil {
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
