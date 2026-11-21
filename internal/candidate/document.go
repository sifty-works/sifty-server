package candidate

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
)

type DocumentItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	CharCount   int    `json:"charCount"`
	State       string `json:"state"`
	StateReason string `json:"stateReason,omitempty"`
	Evidence    bool   `json:"evidence"`
	CreatedAt   string `json:"createdAt"`
}

type DocumentListResp struct {
	Documents []DocumentItem `json:"documents"`
}

type DocumentPatchReq struct {
	Evidence *bool `json:"evidence,omitempty"`
}

type documentRow struct {
	ID          int64
	Name        string
	Kind        string
	State       string
	StateReason string
	CharCount   int
	Evidence    bool
	Text        string
	FileName    string
	CreatedAt   string
}

func kindFromName(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "pdf", true
	case ".docx":
		return "docx", true
	case ".md":
		return "md", true
	case ".txt":
		return "txt", true
	}
	return "", false
}

func checkDocumentBytes(kind string, body []byte) error {
	detected := http.DetectContentType(body)
	switch kind {
	case "txt":
		if !strings.HasPrefix(detected, "text/plain") {
			return apperr.Invalid{Message: "The file must be a plain text file."}
		}
	case "md":
		if !strings.HasPrefix(detected, "text/plain") {
			return apperr.Invalid{Message: "The file must be a plain text file."}
		}
	case "pdf":
		if detected != "application/pdf" {
			return apperr.Invalid{Message: "The file must be a PDF document."}
		}
	case "docx":
		if detected != "application/zip" {
			return apperr.Invalid{Message: "The file must be a Word document."}
		}
	}
	return nil
}

func extractText(ctx context.Context, src TextSource, logger *slog.Logger, kind string, body []byte) (text, state, stateReason string) {
	switch kind {
	case "txt", "md":
		return string(body), "ready", ""
	case "docx":
		t, err := extractDocx(body)
		if err != nil {
			return "", "error", "The document could not be read."
		}
		return t, "ready", ""
	case "pdf":
		return extractPDF(ctx, src, logger, body)
	}
	return "", "error", "Unknown document kind."
}

const (
	wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	mcNS   = "http://schemas.openxmlformats.org/markup-compatibility/2006"
)

func extractDocx(body []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", fmt.Errorf("not a valid Word document")
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("could not read document content")
		}
		defer rc.Close()
		return parseDocxXML(rc)
	}
	return "", fmt.Errorf("not a valid Word document")
}

func parseDocxXML(r io.Reader) (string, error) {
	var sb strings.Builder
	dec := xml.NewDecoder(r)
	inText := false
	skip := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("could not parse document content")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == mcNS && t.Name.Local == "Fallback" {
				skip++
				break
			}
			if skip > 0 {
				break
			}
			if t.Name.Space == wordNS {
				switch t.Name.Local {
				case "t":
					inText = true
				case "tab":
					sb.WriteByte('\t')
				case "br":
					sb.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if t.Name.Space == mcNS && t.Name.Local == "Fallback" {
				if skip > 0 {
					skip--
				}
				break
			}
			if skip > 0 {
				break
			}
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				sb.WriteByte('\n')
			}
		case xml.CharData:
			if inText && skip == 0 {
				sb.Write([]byte(t))
			}
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func rowToItem(row documentRow) DocumentItem {
	return DocumentItem{
		ID:          row.ID,
		Name:        row.Name,
		Kind:        row.Kind,
		CharCount:   row.CharCount,
		State:       row.State,
		StateReason: row.StateReason,
		Evidence:    row.Evidence,
		CreatedAt:   row.CreatedAt,
	}
}

func saveDocument(ctx context.Context, database *db.DB, filesDir, name, kind string, body []byte, text, state, stateReason string) (DocumentItem, error) {
	charCount := len([]rune(text))
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := database.ExecContext(ctx,
		`INSERT INTO document (name, kind, state, state_reason, char_count, evidence, text, file_name, created_at) VALUES (?, ?, ?, ?, ?, 0, ?, '', ?)`,
		name, kind, state, stateReason, charCount, text, now)
	if err != nil {
		return DocumentItem{}, fmt.Errorf("candidate: save document: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return DocumentItem{}, fmt.Errorf("candidate: document id: %w", err)
	}
	if len(body) > 0 {
		if err := os.MkdirAll(filesDir, 0700); err != nil {
			database.ExecContext(ctx, `DELETE FROM document WHERE id=?`, id)
			return DocumentItem{}, fmt.Errorf("candidate: document dir: %w", err)
		}
		fileName := fmt.Sprintf("doc_%d", id)
		if err := os.WriteFile(filepath.Join(filesDir, fileName), body, 0600); err != nil {
			database.ExecContext(ctx, `DELETE FROM document WHERE id=?`, id)
			return DocumentItem{}, fmt.Errorf("candidate: write document file: %w", err)
		}
		if _, err := database.ExecContext(ctx, `UPDATE document SET file_name=? WHERE id=?`, fileName, id); err != nil {
			os.Remove(filepath.Join(filesDir, fileName))
			database.ExecContext(ctx, `DELETE FROM document WHERE id=?`, id)
			return DocumentItem{}, fmt.Errorf("candidate: update document file_name: %w", err)
		}
	}
	return DocumentItem{
		ID:          id,
		Name:        name,
		Kind:        kind,
		CharCount:   charCount,
		State:       state,
		StateReason: stateReason,
		Evidence:    false,
		CreatedAt:   now,
	}, nil
}

func loadDocumentRow(ctx context.Context, database *db.DB, id int64) (documentRow, error) {
	var row documentRow
	var evidence int
	err := database.QueryRowContext(ctx,
		`SELECT id, name, kind, state, state_reason, char_count, evidence, text, file_name, created_at FROM document WHERE id=?`, id).
		Scan(&row.ID, &row.Name, &row.Kind, &row.State, &row.StateReason, &row.CharCount, &evidence, &row.Text, &row.FileName, &row.CreatedAt)
	if err == sql.ErrNoRows {
		return documentRow{}, apperr.NotFound{Message: "Document not found."}
	}
	if err != nil {
		return documentRow{}, fmt.Errorf("candidate: load document %d: %w", id, err)
	}
	row.Evidence = evidence != 0
	return row, nil
}

func listDocumentsFromDB(ctx context.Context, database *db.DB) (DocumentListResp, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT id, name, kind, state, state_reason, char_count, evidence, text, file_name, created_at FROM document ORDER BY id DESC`)
	if err != nil {
		return DocumentListResp{}, fmt.Errorf("candidate: list documents: %w", err)
	}
	defer rows.Close()
	var items []DocumentItem
	for rows.Next() {
		var row documentRow
		var evidence int
		if err := rows.Scan(&row.ID, &row.Name, &row.Kind, &row.State, &row.StateReason, &row.CharCount, &evidence, &row.Text, &row.FileName, &row.CreatedAt); err != nil {
			return DocumentListResp{}, fmt.Errorf("candidate: scan document: %w", err)
		}
		row.Evidence = evidence != 0
		items = append(items, rowToItem(row))
	}
	if err := rows.Err(); err != nil {
		return DocumentListResp{}, fmt.Errorf("candidate: list documents: %w", err)
	}
	if items == nil {
		items = []DocumentItem{}
	}
	return DocumentListResp{Documents: items}, nil
}

func deleteDocument(ctx context.Context, database *db.DB, filesDir string, id int64) error {
	row, err := loadDocumentRow(ctx, database, id)
	if err != nil {
		return err
	}
	if row.FileName != "" && filesDir != "" {
		path := filepath.Join(filesDir, row.FileName)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("candidate: delete document file: %w", err)
		}
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM document WHERE id=?`, id); err != nil {
		return fmt.Errorf("candidate: delete document: %w", err)
	}
	return nil
}

func updateDocumentText(ctx context.Context, database *db.DB, id int64, text, state, stateReason string) error {
	_, err := database.ExecContext(ctx,
		`UPDATE document SET text=?, char_count=?, state=?, state_reason=? WHERE id=?`,
		text, len([]rune(text)), state, stateReason, id)
	if err != nil {
		return fmt.Errorf("candidate: update document text: %w", err)
	}
	return nil
}

func DocumentText(ctx context.Context, database *db.DB, id int64) (string, bool, error) {
	row, err := loadDocumentRow(ctx, database, id)
	if err != nil {
		if _, ok := err.(apperr.NotFound); ok {
			return "", false, nil
		}
		return "", false, err
	}
	return row.Text, true, nil
}

func LoadProfileData(ctx context.Context, database *db.DB, profileID int64) (ProfileData, error) {
	return loadProfileRow(ctx, database, profileID)
}

func setDocumentEvidence(ctx context.Context, database *db.DB, id int64, evidence bool) (DocumentItem, error) {
	ev := 0
	if evidence {
		ev = 1
	}
	res, err := database.ExecContext(ctx, `UPDATE document SET evidence=? WHERE id=?`, ev, id)
	if err != nil {
		return DocumentItem{}, fmt.Errorf("candidate: set evidence: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return DocumentItem{}, apperr.NotFound{Message: "Document not found."}
	}
	row, err := loadDocumentRow(ctx, database, id)
	if err != nil {
		return DocumentItem{}, err
	}
	return rowToItem(row), nil
}
