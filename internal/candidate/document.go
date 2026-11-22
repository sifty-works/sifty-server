package candidate

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
)

type DocumentItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	CharCount   int    `json:"charCount"`
	State       string `json:"state"`
	StateReason string `json:"stateReason,omitempty"`
	Evidence    bool   `json:"evidence"`
	Address     string `json:"address,omitempty"`
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
	Address     string
	CreatedAt   string
}

type Readers struct {
	PDF   TextSource
	Pages fetch.Fetcher
}

type documentKind struct {
	ext      string
	kind     string
	field    string
	key      func(typed string) (string, error)
	detected string
	refusal  string
	read     func(context.Context, Readers, *slog.Logger, []byte) (string, string, string, error)
	source   func(filesDir string, row documentRow) ([]byte, error)
}

var documentKinds = []documentKind{
	{ext: ".pdf", kind: "pdf", detected: "application/pdf", refusal: "The file must be a PDF document.", read: readPDF, source: pdfSource},
	{ext: ".md", kind: "md", detected: "text/plain", refusal: "The file must be a plain text file.", read: readPlainText},
	{ext: ".txt", kind: "txt", detected: "text/plain", refusal: "The file must be a plain text file.", read: readPlainText},
	{kind: "text", field: "text", read: readPlainText},
	{kind: "text", field: "url", key: linkKey, read: readLink, source: linkSource},
}

const notADocumentKind = "The file must be a PDF, Markdown or plain text file."

func kindFromName(name string) (documentKind, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	for _, k := range documentKinds {
		if ext != "" && k.ext == ext {
			return k, true
		}
	}
	return documentKind{}, false
}

func kindFromBody(body map[string]any) (documentKind, string, bool) {
	for _, k := range documentKinds {
		if v, ok := body[k.field].(string); k.field != "" && ok {
			return k, v, true
		}
	}
	return documentKind{}, "", false
}

func kindOfRow(row documentRow) (documentKind, bool) {
	for _, k := range documentKinds {
		if k.kind == row.Kind && (k.key != nil) == (row.Address != "") {
			return k, true
		}
	}
	return documentKind{}, false
}

func (k documentKind) check(body []byte) error {
	if !strings.HasPrefix(http.DetectContentType(body), k.detected) {
		return apperr.Invalid{Message: k.refusal}
	}
	return nil
}

func readPDF(ctx context.Context, r Readers, logger *slog.Logger, body []byte) (string, string, string, error) {
	text, state, reason := extractPDF(ctx, r.PDF, logger, body)
	return text, state, reason, nil
}

func readPlainText(_ context.Context, _ Readers, _ *slog.Logger, body []byte) (string, string, string, error) {
	return string(body), "ready", "", nil
}

func storedFile(filesDir string, row documentRow) ([]byte, error) {
	if row.FileName == "" || filesDir == "" {
		return nil, apperr.NotFound{Message: "No original file for this document."}
	}
	body, err := os.ReadFile(filepath.Join(filesDir, row.FileName))
	if err != nil {
		return nil, apperr.NotFound{Message: "No original file for this document."}
	}
	return body, nil
}

func pdfSource(filesDir string, row documentRow) ([]byte, error) {
	if strings.TrimSpace(row.Text) != "" {
		return nil, nil
	}
	return storedFile(filesDir, row)
}

func addDocument(ctx context.Context, database *db.DB, filesDir string, readers Readers, logger *slog.Logger, k documentKind, name string, source []byte) (DocumentItem, error) {
	var address, key string
	if k.key != nil {
		address = string(source)
		var err error
		if key, err = k.key(address); err != nil {
			return DocumentItem{}, err
		}
		if item, found, err := documentByKey(ctx, database, key); err != nil || found {
			return item, err
		}
		if name == "" {
			name = strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://")
		}
	}
	if name == "" {
		return DocumentItem{}, apperr.Invalid{Message: "Name must not be empty."}
	}
	if err := k.check(source); err != nil {
		return DocumentItem{}, err
	}
	text, state, reason, err := k.read(ctx, readers, logger, source)
	if err != nil {
		return DocumentItem{}, err
	}
	var file []byte
	if k.ext != "" {
		file = source
	}
	item, err := saveDocument(ctx, database, filesDir, name, k.kind, address, key, file, text, state, reason)
	if err != nil && key != "" {
		if existing, found, lerr := documentByKey(ctx, database, key); lerr == nil && found {
			return existing, nil
		}
	}
	return item, err
}

func refreshDocument(ctx context.Context, database *db.DB, filesDir string, readers Readers, logger *slog.Logger, row documentRow) (DocumentItem, error) {
	k, ok := kindOfRow(row)
	if !ok || k.source == nil {
		return DocumentItem{}, apperr.Invalid{Message: "This document cannot be read again."}
	}
	if row.State == "analysing" {
		return DocumentItem{}, apperr.Conflict{Message: "This document is being analysed."}
	}
	source, err := k.source(filesDir, row)
	if err != nil {
		return DocumentItem{}, err
	}
	if source == nil {
		return rowToItem(row), nil
	}
	text, state, reason, err := k.read(ctx, readers, logger, source)
	if err != nil {
		return DocumentItem{}, err
	}
	if state == "ready" && text == row.Text {
		return rowToItem(row), nil
	}
	if err := updateDocumentText(ctx, database, row.ID, text, state, reason); err != nil {
		return DocumentItem{}, err
	}
	if _, err := database.ExecContext(ctx, `UPDATE job SET params=NULL WHERE kind='analyze_document' AND subject=?`, strconv.FormatInt(row.ID, 10)); err != nil {
		return DocumentItem{}, fmt.Errorf("candidate: drop saved analysis: %w", err)
	}
	row, err = loadDocumentRow(ctx, database, row.ID)
	if err != nil {
		return DocumentItem{}, err
	}
	return rowToItem(row), nil
}

func readWordDocument(_ context.Context, _ Readers, _ *slog.Logger, body []byte) (string, string, string, error) {
	t, err := extractDocx(body)
	if err != nil {
		return "", "error", "The document could not be read.", nil
	}
	return t, "ready", "", nil
}

const (
	wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	mcNS   = "http://schemas.openxmlformats.org/markup-compatibility/2006"
)

const docxMain = "word/document.xml"

func extractDocx(body []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", fmt.Errorf("not a valid Word document")
	}
	if _, err := fs.Stat(zr, docxMain); err != nil {
		return "", fmt.Errorf("not a valid Word document")
	}
	targets := map[string]string{}
	for _, e := range docxElements(zr, "word/_rels/document.xml.rels", "Relationship") {
		targets[xmlAttr(e, "Id")] = xmlAttr(e, "Target")
	}
	var around [2][]string
	for _, e := range docxElements(zr, docxMain, "headerReference", "footerReference") {
		target := targets[xmlAttr(e, "id")]
		if target == "" {
			continue
		}
		name := path.Join("word", target)
		if strings.HasPrefix(target, "/") {
			name = target[1:]
		}
		side := 0
		if e.Name.Local == "footerReference" {
			side = 1
		}
		around[side] = append(around[side], name)
	}
	var texts []string
	seen := map[string]bool{}
	collect := func(names []string) {
		for _, name := range names {
			t, err := docxText(zr, name)
			if err != nil || t == "" || seen[t] {
				continue
			}
			seen[t] = true
			texts = append(texts, t)
		}
	}
	collect(around[0])
	main, err := docxText(zr, docxMain)
	if err != nil {
		return "", err
	}
	if main != "" {
		texts = append(texts, main)
	}
	collect(around[1])
	return strings.Join(texts, "\n"), nil
}

func docxText(zr *zip.Reader, name string) (string, error) {
	f, err := zr.Open(name)
	if err != nil {
		return "", fmt.Errorf("could not read document content")
	}
	defer f.Close()
	return parseDocxXML(f)
}

func docxElements(zr *zip.Reader, name string, locals ...string) []xml.StartElement {
	f, err := zr.Open(name)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []xml.StartElement
	dec := xml.NewDecoder(f)
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		if t, ok := tok.(xml.StartElement); ok && slices.Contains(locals, t.Name.Local) {
			out = append(out, t.Copy())
		}
	}
}

func xmlAttr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
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
				case "br", "cr":
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
		Address:     row.Address,
		CreatedAt:   row.CreatedAt,
	}
}

func saveDocument(ctx context.Context, database *db.DB, filesDir, name, kind, address, key string, body []byte, text, state, stateReason string) (DocumentItem, error) {
	charCount := len([]rune(text))
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := database.ExecContext(ctx,
		`INSERT INTO document (name, kind, state, state_reason, char_count, evidence, text, file_name, address, address_key, created_at) VALUES (?, ?, ?, ?, ?, 0, ?, '', ?, ?, ?)`,
		name, kind, state, stateReason, charCount, text, address, key, now)
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
		Address:     address,
		CreatedAt:   now,
	}, nil
}

func loadDocumentRow(ctx context.Context, database *db.DB, id int64) (documentRow, error) {
	var row documentRow
	var evidence int
	err := database.QueryRowContext(ctx,
		`SELECT id, name, kind, state, state_reason, char_count, evidence, text, file_name, address, created_at FROM document WHERE id=?`, id).
		Scan(&row.ID, &row.Name, &row.Kind, &row.State, &row.StateReason, &row.CharCount, &evidence, &row.Text, &row.FileName, &row.Address, &row.CreatedAt)
	if err == sql.ErrNoRows {
		return documentRow{}, apperr.NotFound{Message: "Document not found."}
	}
	if err != nil {
		return documentRow{}, fmt.Errorf("candidate: load document %d: %w", id, err)
	}
	row.Evidence = evidence != 0
	return row, nil
}

func documentByKey(ctx context.Context, database *db.DB, key string) (DocumentItem, bool, error) {
	var id int64
	err := database.QueryRowContext(ctx, `SELECT id FROM document WHERE address_key=?`, key).Scan(&id)
	if err == sql.ErrNoRows {
		return DocumentItem{}, false, nil
	}
	if err != nil {
		return DocumentItem{}, false, fmt.Errorf("candidate: find document by address: %w", err)
	}
	row, err := loadDocumentRow(ctx, database, id)
	return rowToItem(row), err == nil, err
}

func listDocumentsFromDB(ctx context.Context, database *db.DB) (DocumentListResp, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT id, name, kind, state, state_reason, char_count, evidence, text, file_name, address, created_at FROM document ORDER BY id DESC`)
	if err != nil {
		return DocumentListResp{}, fmt.Errorf("candidate: list documents: %w", err)
	}
	defer rows.Close()
	var items []DocumentItem
	for rows.Next() {
		var row documentRow
		var evidence int
		if err := rows.Scan(&row.ID, &row.Name, &row.Kind, &row.State, &row.StateReason, &row.CharCount, &evidence, &row.Text, &row.FileName, &row.Address, &row.CreatedAt); err != nil {
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
