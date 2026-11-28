package candidate

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
)

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type profileRow struct {
	id   int64
	data string
}

type evidenceRow struct {
	id    int64
	state string
	chars int
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryAll[T any](ctx context.Context, q querier, what, query string, scan func(rowScanner) (T, error), args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("candidate: %s: %w", what, err)
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("candidate: scan %s: %w", what, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("candidate: %s: %w", what, err)
	}
	return out, nil
}

func beginTx(ctx context.Context, database *db.DB) (*sql.Tx, error) {
	return database.BeginTx(ctx, nil)
}

func run(ctx context.Context, x execer, what, query string, args ...any) (sql.Result, error) {
	res, err := x.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("candidate: %s: %w", what, err)
	}
	return res, nil
}

func insertRow(ctx context.Context, x execer, what, query string, args ...any) (int64, error) {
	res, err := run(ctx, x, what, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func affected(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func lookup(err error, what string, missing error) error {
	if err == sql.ErrNoRows {
		return missing
	}
	if err != nil {
		return fmt.Errorf("candidate: %s: %w", what, err)
	}
	return nil
}

func selectMe(ctx context.Context, database *db.DB) (string, bool, error) {
	var data string
	err := database.QueryRowContext(ctx, `SELECT data FROM me WHERE id=1`).Scan(&data)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("candidate: load me: %w", err)
	}
	return data, true, nil
}

func upsertMe(ctx context.Context, x execer, data string) error {
	_, err := run(ctx, x, "save me", `INSERT OR REPLACE INTO me (id, data) VALUES (1, ?)`, data)
	return err
}

func selectActiveProfileSetting(ctx context.Context, database *db.DB) (int64, bool) {
	var v string
	if database.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='active_profile'`).Scan(&v) != nil {
		return 0, false
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, database.QueryRowContext(ctx, `SELECT id FROM profile WHERE id=?`, id).Scan(&id) == nil
}

func setActiveProfile(ctx context.Context, database *db.DB, id int64) error {
	_, err := run(ctx, database, "set active profile",
		`INSERT OR REPLACE INTO settings (key, value) VALUES ('active_profile', ?)`, strconv.FormatInt(id, 10))
	return err
}

func firstProfileID(ctx context.Context, database *db.DB) (int64, error) {
	var id int64
	err := lookup(database.QueryRowContext(ctx, `SELECT id FROM profile ORDER BY id LIMIT 1`).Scan(&id), "first profile", apperr.NotFound{Message: "Profile not found."})
	return id, err
}

func selectProfileData(ctx context.Context, database *db.DB, id int64) (string, error) {
	var data string
	err := lookup(database.QueryRowContext(ctx, `SELECT data FROM profile WHERE id=?`, id).Scan(&data), "load profile", apperr.NotFound{Message: "Profile not found."})
	return data, err
}

func selectProfiles(ctx context.Context, database *db.DB) ([]profileRow, error) {
	return queryAll(ctx, database, "list profiles", `SELECT id, data FROM profile ORDER BY id`, func(r rowScanner) (profileRow, error) {
		var p profileRow
		err := r.Scan(&p.id, &p.data)
		return p, err
	})
}

func insertProfile(ctx context.Context, database *db.DB, data string) (int64, error) {
	return insertRow(ctx, database, "create profile", `INSERT INTO profile (data) VALUES (?)`, data)
}

func updateProfileData(ctx context.Context, x execer, id int64, data string) error {
	_, err := run(ctx, x, "save profile", `UPDATE profile SET data=? WHERE id=?`, data, id)
	return err
}

func deleteProfileCascade(ctx context.Context, tx *sql.Tx, id int64) error {
	for _, q := range []string{
		`DELETE FROM exclusion WHERE profile_id=?`,
		`DELETE FROM decision WHERE profile=?`,
		`DELETE FROM profile WHERE id=?`,
	} {
		if _, err := run(ctx, tx, "delete profile", q, id); err != nil {
			return err
		}
	}
	return nil
}

func selectQAs(ctx context.Context, database *db.DB) ([]QAItem, error) {
	return queryAll(ctx, database, "list qa", `SELECT id, question, answer, created_at, updated_at, company FROM qa ORDER BY id`, func(r rowScanner) (QAItem, error) {
		var it QAItem
		err := r.Scan(&it.ID, &it.Question, &it.Answer, &it.CreatedAt, &it.UpdatedAt, &it.Company)
		return it, err
	})
}

func selectQA(ctx context.Context, database *db.DB, id int64) (QAItem, error) {
	var it QAItem
	err := lookup(database.QueryRowContext(ctx,
		`SELECT id, question, answer, created_at, updated_at, company FROM qa WHERE id=?`, id).
		Scan(&it.ID, &it.Question, &it.Answer, &it.CreatedAt, &it.UpdatedAt, &it.Company), "load qa", apperr.NotFound{Message: "Q&A entry not found."})
	return it, err
}

func insertQA(ctx context.Context, database *db.DB, question, answer, company, now string) (int64, error) {
	return insertRow(ctx, database, "create qa",
		`INSERT INTO qa (question, answer, company, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		question, answer, company, now, now)
}

func updateQA(ctx context.Context, database *db.DB, it QAItem) error {
	_, err := run(ctx, database, "update qa",
		`UPDATE qa SET question=?, answer=?, company=?, updated_at=? WHERE id=?`, it.Question, it.Answer, it.Company, it.UpdatedAt, it.ID)
	return err
}

func deleteQA(ctx context.Context, database *db.DB, id int64) error {
	n, err := affected(run(ctx, database, "delete qa", `DELETE FROM qa WHERE id=?`, id))
	if err == nil && n == 0 {
		return apperr.NotFound{Message: "Q&A entry not found."}
	}
	return err
}

const documentCols = `id, name, kind, state, state_reason, char_count, evidence, text, file_name, address, created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDocument(s rowScanner) (documentRow, error) {
	var row documentRow
	err := s.Scan(&row.ID, &row.Name, &row.Kind, &row.State, &row.StateReason, &row.CharCount, &row.Evidence, &row.Text, &row.FileName, &row.Address, &row.CreatedAt)
	return row, err
}

func loadDocumentRow(ctx context.Context, database *db.DB, id int64) (documentRow, error) {
	row, err := scanDocument(database.QueryRowContext(ctx, `SELECT `+documentCols+` FROM document WHERE id=?`, id))
	if err = lookup(err, "load document", apperr.NotFound{Message: "Document not found."}); err != nil {
		return documentRow{}, err
	}
	return row, nil
}

func selectDocumentByKey(ctx context.Context, database *db.DB, key string) (documentRow, bool, error) {
	row, err := scanDocument(database.QueryRowContext(ctx, `SELECT `+documentCols+` FROM document WHERE address_key=?`, key))
	if err == sql.ErrNoRows {
		return documentRow{}, false, nil
	}
	if err != nil {
		return documentRow{}, false, fmt.Errorf("candidate: find document by address: %w", err)
	}
	return row, true, nil
}

func selectDocuments(ctx context.Context, database *db.DB) ([]documentRow, error) {
	return queryAll(ctx, database, "list documents", `SELECT `+documentCols+` FROM document ORDER BY id DESC`, scanDocument)
}

func insertDocument(ctx context.Context, database *db.DB, d documentRow, key string) (int64, error) {
	return insertRow(ctx, database, "save document",
		`INSERT INTO document (name, kind, state, state_reason, char_count, evidence, text, file_name, address, address_key, created_at) VALUES (?, ?, ?, ?, ?, 0, ?, '', ?, ?, ?)`,
		d.Name, d.Kind, d.State, d.StateReason, d.CharCount, d.Text, d.Address, key, d.CreatedAt)
}

func updateDocumentFileName(ctx context.Context, database *db.DB, id int64, fileName string) error {
	_, err := run(ctx, database, "update document file_name", `UPDATE document SET file_name=? WHERE id=?`, fileName, id)
	return err
}

func deleteDocumentRow(ctx context.Context, database *db.DB, id int64) error {
	_, err := run(ctx, database, "delete document", `DELETE FROM document WHERE id=?`, id)
	return err
}

func updateDocumentText(ctx context.Context, database *db.DB, id int64, text, state, stateReason string) error {
	_, err := run(ctx, database, "update document text",
		`UPDATE document SET text=?, char_count=?, state=?, state_reason=? WHERE id=?`,
		text, len([]rune(text)), state, stateReason, id)
	return err
}

func updateDocumentEvidence(ctx context.Context, database *db.DB, id int64, evidence bool) error {
	n, err := affected(run(ctx, database, "set evidence", `UPDATE document SET evidence=? WHERE id=?`, evidence, id))
	if err == nil && n == 0 {
		return apperr.NotFound{Message: "Document not found."}
	}
	return err
}

func updateDocumentState(ctx context.Context, database *db.DB, id int64, state, reason string) error {
	_, err := run(ctx, database, "set document state", `UPDATE document SET state=?, state_reason=? WHERE id=?`, state, reason, id)
	return err
}

func updateDocumentAnalysisCancelled(ctx context.Context, database *db.DB, id int64) error {
	_, err := run(ctx, database, "set cancelled",
		`UPDATE document SET state='ready', state_reason='Analysis cancelled.' WHERE id=? AND state='analysing'`, id)
	return err
}

func ResetInterruptedAnalyses(ctx context.Context, database *db.DB) (int64, error) {
	return affected(run(ctx, database, "reset interrupted analyses",
		`UPDATE document SET state='ready', state_reason='Analysis was interrupted. Press Analyze to continue.'
		 WHERE state='analysing'
		   AND NOT EXISTS (
		     SELECT 1 FROM job
		     WHERE kind='analyze_document' AND subject=CAST(document.id AS TEXT) AND state='queued')`))
}

func clearAnalysisParams(ctx context.Context, database *db.DB, subject string) error {
	_, err := run(ctx, database, "drop saved analysis", `UPDATE job SET params=NULL WHERE kind='analyze_document' AND subject=?`, subject)
	return err
}

func EvidenceDocuments(ctx context.Context, database *db.DB) ([]string, error) {
	return queryAll(ctx, database, "evidence documents", `SELECT text FROM document WHERE evidence=1 AND state='ready' ORDER BY id`, func(r rowScanner) (string, error) {
		var text string
		err := r.Scan(&text)
		return text, err
	})
}

func selectEvidenceRows(ctx context.Context, database *db.DB) ([]evidenceRow, error) {
	return queryAll(ctx, database, "evidence signature", `SELECT id, state, char_count FROM document WHERE evidence=1 ORDER BY id`, func(r rowScanner) (evidenceRow, error) {
		var e evidenceRow
		err := r.Scan(&e.id, &e.state, &e.chars)
		return e, err
	})
}

func selectAnsweredProposalKeys(ctx context.Context, tx *sql.Tx, docID int64) ([]RawFact, error) {
	return queryAll(ctx, tx, "answered proposals", `SELECT field, value FROM proposal WHERE document_id=? AND to_review=0`, func(r rowScanner) (RawFact, error) {
		var k RawFact
		err := r.Scan(&k.Field, &k.Value)
		return k, err
	}, docID)
}

func deleteReviewProposals(ctx context.Context, tx *sql.Tx, docID int64) error {
	_, err := run(ctx, tx, "clear proposals", `DELETE FROM proposal WHERE document_id=? AND to_review=1`, docID)
	return err
}

func insertProposal(ctx context.Context, tx *sql.Tx, docID int64, f RawFact, now string) error {
	_, err := run(ctx, tx, "insert proposal",
		`INSERT INTO proposal (document_id, field, value, quote, to_review, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		docID, f.Field, f.Value, f.Quote, now)
	return err
}

func selectProposalsToReview(ctx context.Context, database *db.DB) ([]ProposalItem, error) {
	return queryAll(ctx, database, "list proposals", `
		SELECT p.id, p.document_id, COALESCE(d.name,'(file deleted)'), p.field, p.value, p.quote, p.created_at
		FROM proposal p
		LEFT JOIN document d ON d.id = p.document_id
		WHERE p.to_review = 1
		ORDER BY p.id DESC`, func(r rowScanner) (ProposalItem, error) {
		var it ProposalItem
		err := r.Scan(&it.ID, &it.DocumentID, &it.DocumentName, &it.Field, &it.Value, &it.Quote, &it.CreatedAt)
		return it, err
	})
}

func loadWaitingProposal(ctx context.Context, database *db.DB, id int64) (waitingProposal, error) {
	p := waitingProposal{id: id}
	var toReview int
	err := lookup(database.QueryRowContext(ctx, `SELECT field, value, to_review FROM proposal WHERE id=?`, id).Scan(&p.field, &p.value, &toReview), "load proposal", apperr.NotFound{Message: "Suggestion not found."})
	if err == nil && toReview == 0 {
		err = apperr.Conflict{Message: "This suggestion was already answered."}
	}
	return p, err
}

func markAnswered(ctx context.Context, x execer, id int64) error {
	n, err := affected(run(ctx, x, "answer proposal", `UPDATE proposal SET to_review=0 WHERE id=? AND to_review=1`, id))
	if err == nil && n != 1 {
		return apperr.Conflict{Message: "This suggestion was already answered."}
	}
	return err
}
