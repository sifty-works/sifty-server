package candidate

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
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
}

type ProposalListResp struct {
	Proposals []ProposalItem `json:"proposals"`
}

func SaveProposals(ctx context.Context, database *db.DB, docID int64, facts []RawFact) error {
	return saveProposals(ctx, database, docID, facts)
}

func UpdateDocumentAnalysed(ctx context.Context, database *db.DB, docID int64, factCount int) error {
	return updateDocumentAnalysed(ctx, database, docID, factCount)
}

func UpdateDocumentError(ctx context.Context, database *db.DB, docID int64, reason string) error {
	return updateDocumentError(ctx, database, docID, reason)
}

func saveProposals(ctx context.Context, database *db.DB, docID int64, facts []RawFact) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("candidate: begin save proposals: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM proposal WHERE document_id=? AND to_review=1`, docID); err != nil {
		return fmt.Errorf("candidate: clear proposals: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, f := range facts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO proposal (document_id, field, value, quote, to_review, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
			docID, f.Field, f.Value, f.Quote, now); err != nil {
			return fmt.Errorf("candidate: insert proposal: %w", err)
		}
	}
	return tx.Commit()
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

func updateDocumentError(ctx context.Context, database *db.DB, docID int64, reason string) error {
	_, err := database.ExecContext(ctx,
		`UPDATE document SET state='error', state_reason=? WHERE id=?`, reason, docID)
	return err
}

func listProposals(ctx context.Context, database *db.DB) (ProposalListResp, error) {
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
	var items []ProposalItem
	for rows.Next() {
		var it ProposalItem
		if err := rows.Scan(&it.ID, &it.DocumentID, &it.DocumentName, &it.Field, &it.Value, &it.Quote, &it.CreatedAt); err != nil {
			return ProposalListResp{}, fmt.Errorf("candidate: scan proposal: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return ProposalListResp{}, fmt.Errorf("candidate: list proposals rows: %w", err)
	}
	if items == nil {
		items = []ProposalItem{}
	}
	return ProposalListResp{Proposals: items}, nil
}
