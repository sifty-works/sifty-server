package importv1

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
)

const (
	dropDocumentDeleted   = "deleted"
	dropDocumentDuplicate = "duplicate"

	dropProposalNoDocument = "its document was not carried"
	dropProposalUnreadable = "value could not be read"
	dropProposalOtherField = "a kind of suggestion the server does not propose"
	dropProposalOtherRole  = "target role of a profile that is not the active one"
	dropProposalDuplicate  = "duplicate"
)

type oldDocument struct {
	id          string
	kind        string
	name        string
	storagePath string
	sourceURL   string
	contentHash string
	state       string
	text        string
	errText     string
	createdAt   string
}

var oldDocumentKinds = map[string]string{
	"pdf":      "pdf",
	"docx":     "docx",
	"md":       "md",
	"markdown": "md",
	"txt":      "txt",
	"text":     "txt",
}

func readDocuments(ctx context.Context, oldDB *sql.DB) ([]oldDocument, error) {
	rows, err := oldDB.QueryContext(ctx,
		`SELECT id, kind, original_name, storage_path, source_url, content_hash, processing_state,
		        extracted_text, error, created_at
		 FROM profile_documents ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oldDocument
	for rows.Next() {
		var d oldDocument
		var storage, source, hash, text, errText sql.NullString
		if err := rows.Scan(&d.id, &d.kind, &d.name, &storage, &source, &hash, &d.state,
			&text, &errText, &d.createdAt); err != nil {
			return nil, err
		}
		d.storagePath = strings.TrimSpace(storage.String)
		d.sourceURL = strings.TrimSpace(source.String)
		d.contentHash = strings.TrimSpace(hash.String)
		d.text = text.String
		d.errText = strings.TrimSpace(errText.String)
		out = append(out, d)
	}
	return out, rows.Err()
}

func readDocumentFile(fromDir, storagePath string) ([]byte, error) {
	if storagePath == "" {
		return nil, nil
	}
	rel := filepath.Clean(storagePath)
	if !filepath.IsLocal(rel) {
		return nil, nil
	}
	path := filepath.Join(fromDir, rel)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	return os.ReadFile(path)
}

func newDocumentKind(d oldDocument) string {
	if k, ok := oldDocumentKinds[strings.ToLower(d.kind)]; ok {
		return k
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(d.name)), ".")
	if k, ok := oldDocumentKinds[ext]; ok {
		return k
	}
	if d.storagePath != "" {
		return "txt"
	}
	return "text"
}

func newDocumentState(d oldDocument, fileMissing bool) (string, string) {
	hasText := strings.TrimSpace(d.text) != ""
	switch {
	case fileMissing:
		return "unavailable", "The original file was not found in the old data folder."
	case d.state == "analyzed":
		return "analysed", "0"
	case d.state == "ready", d.state == "imported", d.state == "stale":
		return "ready", ""
	case d.state == "no_text":
		return "unavailable", "No text could be read from this document in the previous version."
	case d.state == "missing":
		return "unavailable", "This document had no file or text in the previous version."
	case d.state == "error":
		if d.errText == "" {
			return "error", "The document could not be read in the previous version."
		}
		return "error", d.errText
	case hasText:
		return "ready", ""
	}
	return "unavailable", "No text was available in the previous version."
}

func documentDedupeKey(d oldDocument, addressKey string, body []byte) string {
	if d.storagePath != "" {
		hash := d.contentHash
		if hash == "" && len(body) > 0 {
			sum := sha256.Sum256(body)
			hash = hex.EncodeToString(sum[:])
		}
		if hash != "" {
			return "file\x00" + hash
		}
	} else if addressKey != "" {
		return "address\x00" + addressKey
	}
	return "text\x00" + d.name + "\x00" + d.text
}

func dropDocument(report *importReport, reason string) {
	if report.DocumentsDropped == nil {
		report.DocumentsDropped = map[string]int{}
	}
	report.DocumentsDropped[reason]++
}

type importedDocuments struct {
	ids      map[string]int64
	analysed map[int64]bool
}

func importDocumentRows(ctx context.Context, tx *sql.Tx, oldDocs []oldDocument, fromDir, dataDir string, report *importReport) (*importedDocuments, error) {
	out := &importedDocuments{ids: map[string]int64{}, analysed: map[int64]bool{}}
	kept := map[string]int64{}
	filesDir := filepath.Join(dataDir, "files")
	now := time.Now().UTC().Format(time.RFC3339)

	for _, d := range oldDocs {
		report.DocumentsRead++
		if d.state == "deleted" {
			dropDocument(report, dropDocumentDeleted)
			continue
		}
		body, err := readDocumentFile(fromDir, d.storagePath)
		if err != nil {
			return nil, fmt.Errorf("reading document file: %w", err)
		}
		fileMissing := d.storagePath != "" && len(body) == 0

		address, addressKey := "", ""
		if d.storagePath == "" && d.sourceURL != "" {
			if key, err := listing.Canonical(d.sourceURL); err == nil {
				address, addressKey = d.sourceURL, key
			}
		}

		dedupeKey := documentDedupeKey(d, addressKey, body)
		if keptID, found := kept[dedupeKey]; found {
			out.ids[d.id] = keptID
			dropDocument(report, dropDocumentDuplicate)
			continue
		}

		name := strings.TrimSpace(d.name)
		if name == "" {
			name = address
		}
		if name == "" {
			name = "Untitled document"
		}
		state, reason := newDocumentState(d, fileMissing)
		createdAt := normDateUTC(d.createdAt)
		if createdAt == "" {
			createdAt = now
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO document (name, kind, state, state_reason, char_count, evidence, text, file_name, address, address_key, created_at)
			 VALUES (?, ?, ?, ?, ?, 0, ?, '', ?, ?, ?)`,
			name, newDocumentKind(d), state, reason, len([]rune(d.text)), d.text, address, addressKey, createdAt)
		if err != nil {
			return nil, fmt.Errorf("inserting document: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("document id: %w", err)
		}
		if len(body) > 0 {
			fileName := fmt.Sprintf("doc_%d", id)
			if err := os.MkdirAll(filesDir, 0700); err != nil {
				return nil, fmt.Errorf("creating files folder: %w", err)
			}
			if err := os.WriteFile(filepath.Join(filesDir, fileName), body, 0600); err != nil {
				return nil, fmt.Errorf("writing document file: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE document SET file_name=? WHERE id=?`, fileName, id); err != nil {
				return nil, fmt.Errorf("setting document file name: %w", err)
			}
			sum := sha256.Sum256(body)
			report.DocumentFiles = append(report.DocumentFiles, fileName+" "+hex.EncodeToString(sum[:]))
		}
		if fileMissing {
			report.DocumentFilesMissing++
		}
		kept[dedupeKey] = id
		out.ids[d.id] = id
		if state == "analysed" {
			out.analysed[id] = true
		}
		report.DocumentsWritten++
	}
	return out, nil
}

type oldFact struct {
	profileID  string
	documentID string
	fieldKey   string
	valueJSON  string
	quote      string
	createdAt  string
}

func readProposedFacts(ctx context.Context, oldDB *sql.DB) ([]oldFact, error) {
	rows, err := oldDB.QueryContext(ctx,
		`SELECT profile_id, document_id, field_key, value_json, quote, created_at
		 FROM profile_facts WHERE state='proposed' ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oldFact
	for rows.Next() {
		var f oldFact
		var document, quote sql.NullString
		if err := rows.Scan(&f.profileID, &document, &f.fieldKey, &f.valueJSON, &quote, &f.createdAt); err != nil {
			return nil, err
		}
		f.documentID = document.String
		f.quote = quote.String
		out = append(out, f)
	}
	return out, rows.Err()
}

var proposalYearRe = regexp.MustCompile(`\d{4}`)

type oldFactItem struct {
	Name    string `json:"name"`
	Level   string `json:"level"`
	Company string `json:"company"`
	Title   string `json:"title"`
	Start   string `json:"start"`
	End     string `json:"end"`
	Current bool   `json:"current"`
}

func withLevel(name, level string) string {
	name, level = strings.TrimSpace(name), strings.TrimSpace(level)
	if name == "" {
		return ""
	}
	if level == "" || strings.EqualFold(level, "unstated") {
		return name
	}
	return name + " (" + level + ")"
}

func experienceValue(it oldFactItem) string {
	title, company := strings.TrimSpace(it.Title), strings.TrimSpace(it.Company)
	if title == "" || company == "" {
		return ""
	}
	value := title + " at " + company
	start, end := proposalYearRe.FindString(it.Start), proposalYearRe.FindString(it.End)
	switch {
	case start != "" && end != "":
		value += ", " + start + "\u2013" + end
	case start != "" && it.Current:
		value += ", " + start + "\u2013present"
	}
	return value
}

func proposalValues(key, valueJSON string) (string, []string, bool) {
	raw := json.RawMessage(valueJSON)
	switch key {
	case "skills", "languages":
		field := "skill"
		if key == "languages" {
			field = "language"
		}
		var it oldFactItem
		if json.Unmarshal(raw, &it) != nil {
			var name string
			if json.Unmarshal(raw, &name) != nil {
				return field, nil, true
			}
			it.Name = name
		}
		level := strings.TrimSpace(it.Level)
		if key == "skills" {
			level = mapSkillLevel(level)
		}
		return field, []string{withLevel(it.Name, level)}, true
	case "experience":
		var it oldFactItem
		if json.Unmarshal(raw, &it) != nil {
			return "experience", []string{""}, true
		}
		return "experience", []string{experienceValue(it)}, true
	case "targetRoles":
		var list []string
		if json.Unmarshal(raw, &list) != nil {
			var one string
			if json.Unmarshal(raw, &one) != nil {
				return "targetRole", nil, true
			}
			list = []string{one}
		}
		var out []string
		for _, role := range list {
			out = append(out, strings.TrimSpace(role))
		}
		return "targetRole", out, true
	}
	return "", nil, false
}

func dropProposal(report *importReport, reason string) {
	if report.ProposalsDropped == nil {
		report.ProposalsDropped = map[string]int{}
	}
	report.ProposalsDropped[reason]++
}

func importProposalRows(ctx context.Context, tx *sql.Tx, facts []oldFact, docs *importedDocuments, activeProfileID int64, report *importReport) (map[int64]int, error) {
	perDocument := map[int64]int{}
	written := map[string]bool{}
	now := time.Now().UTC().Format(time.RFC3339)

	for _, f := range facts {
		field, values, known := proposalValues(f.fieldKey, f.valueJSON)
		if !known || len(values) == 0 {
			values = []string{""}
		}
		for _, value := range values {
			report.ProposalsRead++
			docID, carried := docs.ids[f.documentID]
			switch {
			case !carried:
				dropProposal(report, dropProposalNoDocument)
				continue
			case !known:
				dropProposal(report, dropProposalOtherField)
				if report.ProposalKeysDropped == nil {
					report.ProposalKeysDropped = map[string]int{}
				}
				report.ProposalKeysDropped[f.fieldKey]++
				continue
			case value == "":
				dropProposal(report, dropProposalUnreadable)
				continue
			case field == "targetRole" && (activeProfileID == 0 || report.profileIDs[f.profileID] != activeProfileID):
				dropProposal(report, dropProposalOtherRole)
				continue
			}
			dedupeKey := fmt.Sprintf("%d\x00%s\x00%s", docID, field, strings.ToLower(value))
			if written[dedupeKey] {
				dropProposal(report, dropProposalDuplicate)
				continue
			}
			createdAt := normDateUTC(f.createdAt)
			if createdAt == "" {
				createdAt = now
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO proposal (document_id, field, value, quote, to_review, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
				docID, field, value, f.quote, createdAt); err != nil {
				return nil, fmt.Errorf("inserting proposal: %w", err)
			}
			written[dedupeKey] = true
			perDocument[docID]++
			report.ProposalsWritten++
		}
	}
	return perDocument, nil
}

func importDocuments(ctx context.Context, oldDB *sql.DB, newDB *db.DB, fromDir, dataDir string, report *importReport) error {
	oldDocs, err := readDocuments(ctx, oldDB)
	if err != nil {
		return fmt.Errorf("reading profile_documents: %w", err)
	}
	facts, err := readProposedFacts(ctx, oldDB)
	if err != nil {
		return fmt.Errorf("reading profile_facts: %w", err)
	}
	activeProfileID := report.activeProfileID
	if activeProfileID == 0 {
		if err := newDB.QueryRowContext(ctx, `SELECT id FROM profile ORDER BY id LIMIT 1`).Scan(&activeProfileID); err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("reading the active profile: %w", err)
		}
	}
	tx, err := newDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning documents transaction: %w", err)
	}
	defer tx.Rollback()
	docs, err := importDocumentRows(ctx, tx, oldDocs, fromDir, dataDir, report)
	if err != nil {
		return err
	}
	perDocument, err := importProposalRows(ctx, tx, facts, docs, activeProfileID, report)
	if err != nil {
		return err
	}
	for id := range docs.analysed {
		if _, err := tx.ExecContext(ctx, `UPDATE document SET state_reason=? WHERE id=?`, fmt.Sprintf("%d", perDocument[id]), id); err != nil {
			return fmt.Errorf("setting the proposal count of a document: %w", err)
		}
	}
	return tx.Commit()
}

func sortedCounts(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatDocuments(sb *strings.Builder, r *importReport) {
	dropped := 0
	for _, n := range r.DocumentsDropped {
		dropped += n
	}
	fmt.Fprintf(sb, "Documents: %d read, %d written, %d dropped\n", r.DocumentsRead, r.DocumentsWritten, dropped)
	for _, reason := range sortedCounts(r.DocumentsDropped) {
		fmt.Fprintf(sb, "  dropped (%s): %d\n", reason, r.DocumentsDropped[reason])
	}
	if r.DocumentFilesMissing > 0 {
		fmt.Fprintf(sb, "  carried without their file (file not found in the old folder): %d\n", r.DocumentFilesMissing)
	}
	for _, line := range r.DocumentFiles {
		fmt.Fprintf(sb, "  copied %s\n", line)
	}
	if lost := r.DocumentsRead - r.DocumentsWritten - dropped; lost != 0 {
		fmt.Fprintf(sb, "  warning: %d read but neither written nor counted as dropped\n", lost)
	}
}

func formatProposals(sb *strings.Builder, r *importReport) {
	dropped := 0
	for _, n := range r.ProposalsDropped {
		dropped += n
	}
	fmt.Fprintf(sb, "Proposals: %d read, %d written, %d dropped\n", r.ProposalsRead, r.ProposalsWritten, dropped)
	for _, reason := range sortedCounts(r.ProposalsDropped) {
		fmt.Fprintf(sb, "  dropped (%s): %d\n", reason, r.ProposalsDropped[reason])
	}
	for _, key := range sortedCounts(r.ProposalKeysDropped) {
		fmt.Fprintf(sb, "  dropped field %s: %d\n", key, r.ProposalKeysDropped[key])
	}
	if lost := r.ProposalsRead - r.ProposalsWritten - dropped; lost != 0 {
		fmt.Fprintf(sb, "  warning: %d read but neither written nor counted as dropped\n", lost)
	}
}
