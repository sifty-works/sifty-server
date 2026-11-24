package candidate

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
	if err := clearAnalysisParams(ctx, database, strconv.FormatInt(row.ID, 10)); err != nil {
		return DocumentItem{}, err
	}
	row, err = loadDocumentRow(ctx, database, row.ID)
	if err != nil {
		return DocumentItem{}, err
	}
	return rowToItem(row), nil
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
	row := documentRow{
		Name: name, Kind: kind, State: state, StateReason: stateReason, CharCount: len([]rune(text)),
		Text: text, Address: address, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	id, err := insertDocument(ctx, database, row, key)
	if err != nil {
		return DocumentItem{}, err
	}
	row.ID = id
	if len(body) > 0 {
		if err := os.MkdirAll(filesDir, 0700); err != nil {
			deleteDocumentRow(ctx, database, id)
			return DocumentItem{}, fmt.Errorf("candidate: document dir: %w", err)
		}
		fileName := fmt.Sprintf("doc_%d", id)
		if err := os.WriteFile(filepath.Join(filesDir, fileName), body, 0600); err != nil {
			deleteDocumentRow(ctx, database, id)
			return DocumentItem{}, fmt.Errorf("candidate: write document file: %w", err)
		}
		if err := updateDocumentFileName(ctx, database, id, fileName); err != nil {
			os.Remove(filepath.Join(filesDir, fileName))
			deleteDocumentRow(ctx, database, id)
			return DocumentItem{}, err
		}
	}
	return rowToItem(row), nil
}

func documentByKey(ctx context.Context, database *db.DB, key string) (DocumentItem, bool, error) {
	row, found, err := selectDocumentByKey(ctx, database, key)
	return rowToItem(row), found, err
}

func listDocumentsFromDB(ctx context.Context, database *db.DB) (DocumentListResp, error) {
	rows, err := selectDocuments(ctx, database)
	if err != nil {
		return DocumentListResp{}, err
	}
	items := []DocumentItem{}
	for _, row := range rows {
		items = append(items, rowToItem(row))
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
	return deleteDocumentRow(ctx, database, id)
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

func setDocumentEvidence(ctx context.Context, database *db.DB, id int64, evidence bool) (DocumentItem, error) {
	if err := updateDocumentEvidence(ctx, database, id, evidence); err != nil {
		return DocumentItem{}, err
	}
	row, err := loadDocumentRow(ctx, database, id)
	if err != nil {
		return DocumentItem{}, err
	}
	return rowToItem(row), nil
}
