package candidate_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
)

func TestEvidenceDocumentsAreTheReadyOnesMarkedAsEvidenceAndSignatureFollowsThem(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	insert := func(name, state, text string, evidence int) {
		if _, err := d.Exec(`INSERT INTO document (name, kind, state, char_count, evidence, text, created_at) VALUES (?, 'txt', ?, ?, ?, ?, '2026-11-20T00:00:00Z')`,
			name, state, len([]rune(text)), evidence, text); err != nil {
			t.Fatalf("insert document: %v", err)
		}
	}
	insert("first", "ready", "evidence text one", 1)
	insert("second", "ready", "not marked as evidence", 0)
	insert("third", "unreadable", "", 1)

	docs, err := candidate.EvidenceDocuments(ctx, d)
	if err != nil || len(docs) != 1 || docs[0] != "evidence text one" {
		t.Fatalf("documents = %v, err = %v", docs, err)
	}
	before, err := candidate.EvidenceSignature(ctx, d)
	if err != nil || before == "" {
		t.Fatalf("signature = %q, err = %v", before, err)
	}
	d.Exec(`UPDATE document SET evidence=1 WHERE name='second'`)
	after, _ := candidate.EvidenceSignature(ctx, d)
	if after == before {
		t.Error("marking another document as evidence did not change the signature")
	}
	d.Exec(`UPDATE document SET text='evidence text one, longer', char_count=24 WHERE name='first'`)
	longer, _ := candidate.EvidenceSignature(ctx, d)
	if longer == after {
		t.Error("a changed text length did not change the signature")
	}
}
