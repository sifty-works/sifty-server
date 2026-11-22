package main

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
)

const interruptedReason = "Analysis was interrupted. Press Analyze to continue."

func seedDocument(t *testing.T, d *db.DB, name, state, reason string) int64 {
	t.Helper()
	res, err := d.Exec(`INSERT INTO document (name, kind, state, state_reason, char_count, text, created_at)
		VALUES (?, 'text', ?, ?, 20, 'Go developer, five years.', ?)`,
		name, state, reason, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("seed document: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedAnalyzeJob(t *testing.T, d *db.DB, docID int64, state, notBefore, params string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.Exec(`INSERT INTO job (kind, subject, state, not_before, params, created, updated)
		VALUES ('analyze_document', ?, ?, ?, ?, ?, ?)`,
		fmt.Sprint(docID), state, notBefore, params, now, now); err != nil {
		t.Fatalf("seed job: %v", err)
	}
}

func documentByID(t *testing.T, base string, id int64) map[string]any {
	t.Helper()
	body, _ := apiGet(t, base, "/api/documents")
	docs, _ := body["documents"].([]any)
	for _, d := range docs {
		dm, _ := d.(map[string]any)
		if dm["id"] == float64(id) {
			return dm
		}
	}
	t.Fatalf("document %d not listed", id)
	return nil
}

func TestStartSetsDocumentsWithoutLiveAnalysisBackToReady(t *testing.T) {
	dir := t.TempDir()
	seed, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	checkpoint := `{"nextChunk":3,"facts":[]}`
	stale := seedDocument(t, seed, "stale.txt", "analysing", "")
	seedAnalyzeJob(t, seed, stale, "running", time.Now().UTC().Format(time.RFC3339), checkpoint)
	noJob := seedDocument(t, seed, "nojob.txt", "analysing", "")
	waiting := seedDocument(t, seed, "waiting.txt", "analysing", "")
	seedAnalyzeJob(t, seed, waiting, "queued", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), checkpoint)
	cancelled := seedDocument(t, seed, "cancelled.txt", "ready", "Analysis cancelled.")
	analysed := seedDocument(t, seed, "analysed.txt", "analysed", "2")
	failed := seedDocument(t, seed, "failed.txt", "error", "The analysis could not be completed. Try again.")
	seed.Close()

	port, cancel := startServer(t, dir, true)
	defer cancel()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	for _, id := range []int64{stale, noJob} {
		doc := documentByID(t, base, id)
		if doc["state"] != "ready" || doc["stateReason"] != interruptedReason {
			t.Errorf("document %d = %v %v, want ready with %q", id, doc["state"], doc["stateReason"], interruptedReason)
		}
	}
	if doc := documentByID(t, base, waiting); doc["state"] != "analysing" {
		t.Errorf("document with queued analysis = %v, want analysing", doc["state"])
	}
	for id, want := range map[int64][2]string{
		cancelled: {"ready", "Analysis cancelled."},
		analysed:  {"analysed", "2"},
		failed:    {"error", "The analysis could not be completed. Try again."},
	} {
		doc := documentByID(t, base, id)
		if doc["state"] != want[0] || doc["stateReason"] != want[1] {
			t.Errorf("document %d = %v %v, want %v", id, doc["state"], doc["stateReason"], want)
		}
	}

	probe, err := db.Open(filepath.Join(dir, dbFileName), db.Migrations)
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	defer probe.Close()
	var params string
	probe.QueryRow(`SELECT params FROM job WHERE subject=? AND kind='analyze_document'`, fmt.Sprint(stale)).Scan(&params)
	if params != checkpoint {
		t.Errorf("stored chunks = %q, want %q kept", params, checkpoint)
	}
}
