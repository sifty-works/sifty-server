package importv1_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type oldDoc struct {
	id      string
	profile string
	kind    string
	name    string
	storage string
	url     string
	hash    string
	state   string
	text    string
	errText string
	created string
}

func insertOldDocument(t *testing.T, fromDir string, d oldDoc) {
	t.Helper()
	odb := openOldDB(t, fromDir)
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	if d.profile == "" {
		d.profile = "p1"
	}
	if d.created == "" {
		d.created = "2024-03-01T10:00:00Z"
	}
	_, err := odb.Exec(
		`INSERT INTO profile_documents (id, profile_id, kind, original_name, storage_path, source_url,
		   content_hash, processing_state, extracted_text, error, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.id, d.profile, d.kind, d.name, nullable(d.storage), nullable(d.url), nullable(d.hash),
		d.state, nullable(d.text), nullable(d.errText), d.created, d.created)
	if err != nil {
		t.Fatalf("insertOldDocument %q: %v", d.id, err)
	}
}

type newDoc struct {
	id       int64
	name     string
	kind     string
	state    string
	reason   string
	chars    int
	evidence int
	text     string
	fileName string
	address  string
	created  string
}

func readNewDocuments(t *testing.T, targetDir string) []newDoc {
	t.Helper()
	newDB := openNewDB(t, targetDir)
	rows, err := newDB.Query(
		`SELECT id, name, kind, state, state_reason, char_count, evidence, text, file_name, address, created_at
		 FROM document ORDER BY id`)
	if err != nil {
		t.Fatalf("read documents: %v", err)
	}
	defer rows.Close()
	var out []newDoc
	for rows.Next() {
		var d newDoc
		if err := rows.Scan(&d.id, &d.name, &d.kind, &d.state, &d.reason, &d.chars, &d.evidence, &d.text, &d.fileName, &d.address, &d.created); err != nil {
			t.Fatalf("scan document: %v", err)
		}
		out = append(out, d)
	}
	return out
}

func setupDocumentFixture(t *testing.T) string {
	t.Helper()
	fromDir := makeOldDir(t, "")
	odb := openOldDB(t, fromDir)
	insertProfile(t, odb, "p1", "First", "2024-01-01T00:00:00Z", false)
	insertProfile(t, odb, "p2", "Second", "2024-01-02T00:00:00Z", false)
	return fromDir
}

func TestImport_DocumentWithMissingFileCarriedUnavailableAndCounted(t *testing.T) {
	fromDir := setupDocumentFixture(t)
	insertOldDocument(t, fromDir, oldDoc{id: "d1", kind: "pdf", name: "Lost CV.pdf",
		storage: filepath.Join("evidence", "profile-documents", "d1", "original.pdf"),
		hash:    "h1", state: "ready", text: "Text that survived"})

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	docs := readNewDocuments(t, targetDir)
	if len(docs) != 1 {
		t.Fatalf("documents: got %d, want 1", len(docs))
	}
	d := docs[0]
	if d.state != "unavailable" || d.fileName != "" || d.text != "Text that survived" || d.reason == "" {
		t.Errorf("document: %+v", d)
	}
	if !strings.Contains(report, "without their file (file not found in the old folder): 1") {
		t.Errorf("report: %s", report)
	}
}

func TestImport_StoragePathOutsideOldFolderIsNotRead(t *testing.T) {
	fromDir := setupDocumentFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	for i, path := range []string{outside, "../outside.txt"} {
		insertOldDocument(t, fromDir, oldDoc{id: "o" + string(rune('1'+i)), kind: "txt", name: "outside" + string(rune('1'+i)) + ".txt",
			storage: path, hash: "ho" + string(rune('1'+i)), state: "ready", text: "text"})
	}

	targetDir := t.TempDir()
	runImport(t, fromDir, targetDir)

	for _, d := range readNewDocuments(t, targetDir) {
		if d.fileName != "" || d.state != "unavailable" {
			t.Errorf("document read a path outside the old folder: %+v", d)
		}
	}
}

func insertOldFact(t *testing.T, fromDir, id, profile, document, key, valueJSON, quote, state string) {
	t.Helper()
	odb := openOldDB(t, fromDir)
	var doc any
	if document != "" {
		doc = document
	}
	_, err := odb.Exec(
		`INSERT INTO profile_facts (id, profile_id, document_id, field_key, value_json, quote, origin, state, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,'document',?,'2024-03-10T10:00:00Z','2024-03-10T10:00:00Z')`,
		id, profile, doc, key, valueJSON, quote, state)
	if err != nil {
		t.Fatalf("insertOldFact %q: %v", id, err)
	}
}

type newProposal struct {
	document int64
	field    string
	value    string
	quote    string
	toReview int
}

func readNewProposals(t *testing.T, targetDir string) []newProposal {
	t.Helper()
	newDB := openNewDB(t, targetDir)
	rows, err := newDB.Query(`SELECT document_id, field, value, quote, to_review FROM proposal ORDER BY id`)
	if err != nil {
		t.Fatalf("read proposals: %v", err)
	}
	defer rows.Close()
	var out []newProposal
	for rows.Next() {
		var p newProposal
		if err := rows.Scan(&p.document, &p.field, &p.value, &p.quote, &p.toReview); err != nil {
			t.Fatalf("scan proposal: %v", err)
		}
		out = append(out, p)
	}
	return out
}

func proposalValue(ps []newProposal, field string) []string {
	var out []string
	for _, p := range ps {
		if p.field == field {
			out = append(out, p.value)
		}
	}
	return out
}

func setupProposalFixture(t *testing.T) string {
	t.Helper()
	fromDir := setupDocumentFixture(t)
	setActiveProfileSetting(t, fromDir, "p1")
	insertOldDocument(t, fromDir, oldDoc{id: "d1", profile: "p1", kind: "md", name: "cv.md",
		state: "analyzed", text: "Invented CV", created: "2024-03-01T10:00:00Z"})
	return fromDir
}

func TestImport_ProposedFactBecomesProposalAndDecidedOnesDoNot(t *testing.T) {
	fromDir := setupProposalFixture(t)
	insertOldFact(t, fromDir, "f1", "p1", "d1", "skills", `{"key":"go","name":"Go","level":"Proficient"}`, "Go for ten years", "proposed")
	insertOldFact(t, fromDir, "f2", "p1", "d1", "skills", `{"key":"rust","name":"Rust","level":"Expert"}`, "Rust daily", "accepted")
	insertOldFact(t, fromDir, "f3", "p1", "d1", "skills", `{"key":"zig","name":"Zig","level":"Beginner"}`, "Zig once", "rejected")
	insertOldFact(t, fromDir, "f4", "p1", "d1", "skills", `{"key":"c","name":"C","level":"Expert"}`, "C always", "profile_match")

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	ps := readNewProposals(t, targetDir)
	if len(ps) != 1 {
		t.Fatalf("proposals: got %+v, want one", ps)
	}
	p := ps[0]
	if p.field != "skill" || p.value != "Go (Proficient)" || p.quote != "Go for ten years" || p.toReview != 1 || p.document != 1 {
		t.Errorf("proposal: %+v", p)
	}
	if !strings.Contains(report, "Proposals: 1 read, 1 written, 0 dropped") {
		t.Errorf("report: %s", report)
	}
}

func TestImport_FactFieldsBecomeTheFieldsTheServerReads(t *testing.T) {
	fromDir := setupProposalFixture(t)
	insertOldFact(t, fromDir, "f1", "p1", "d1", "skills", `{"key":"go","name":"Go","level":"Proficient"}`, "q1", "proposed")
	insertOldFact(t, fromDir, "f2", "p1", "d1", "skills", `{"key":"sql","name":"SQL","level":"unstated"}`, "q2", "proposed")
	insertOldFact(t, fromDir, "f3", "p1", "d1", "languages", `{"name":"French","level":"B2"}`, "q3", "proposed")
	insertOldFact(t, fromDir, "f4", "p1", "d1", "languages", `{"name":"German","level":""}`, "q4", "proposed")
	insertOldFact(t, fromDir, "f5", "p1", "d1", "experience",
		`{"company":"Nexus","title":"Lead Engineer","start":"2019-03","end":"2022-06","skills":[]}`, "q5", "proposed")
	insertOldFact(t, fromDir, "f6", "p1", "d1", "experience",
		`{"company":"Orbit","title":"Engineer","start":"03/2023","current":true,"skills":[]}`, "q6", "proposed")
	insertOldFact(t, fromDir, "f7", "p1", "d1", "experience",
		`{"company":"Quill","title":"Intern","skills":[]}`, "q7", "proposed")
	insertOldFact(t, fromDir, "f8", "p1", "d1", "compensation.cashMin", `90000`, "q8", "proposed")
	insertOldFact(t, fromDir, "f9", "p1", "d1", "compensation.cashMin", `95000`, "q9", "proposed")
	insertOldFact(t, fromDir, "f10", "p1", "d1", "targetIndustries", `["Fintech"]`, "q10", "proposed")
	insertOldFact(t, fromDir, "f20", "p1", "d1", "skills", `{"key":"rust","name":"Rust","level":"Familiar"}`, "q20", "proposed")

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	ps := readNewProposals(t, targetDir)
	check := func(field string, want ...string) {
		t.Helper()
		got := proposalValue(ps, field)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: got %q, want %q", field, got, want)
		}
	}
	check("skill", "Go (Proficient)", "SQL", "Rust (Proficient)")
	check("language", "French (B2)", "German")
	check("experience", "Lead Engineer at Nexus, 2019\u20132022", "Engineer at Orbit, 2023\u2013present", "Intern at Quill")
	if len(ps) != 8 {
		t.Errorf("proposals: got %d, want 8", len(ps))
	}
	for _, line := range []string{
		"Proposals: 11 read, 8 written, 3 dropped",
		"dropped (a kind of suggestion the server does not propose): 3",
		"dropped field compensation.cashMin: 2",
		"dropped field targetIndustries: 1",
	} {
		if !strings.Contains(report, line) {
			t.Errorf("report lacks %q:\n%s", line, report)
		}
	}
}

func TestImport_TargetRoleOfActiveProfileCarriedAndOfOtherProfileDropped(t *testing.T) {
	fromDir := setupProposalFixture(t)
	insertOldDocument(t, fromDir, oldDoc{id: "d2", profile: "p2", kind: "md", name: "second.md",
		state: "ready", text: "Second CV", created: "2024-03-02T10:00:00Z"})
	insertOldFact(t, fromDir, "f1", "p1", "d1", "targetRoles", `["Platform Engineer"]`, "q1", "proposed")
	insertOldFact(t, fromDir, "f2", "p2", "d2", "targetRoles", `["Data Engineer"]`, "q2", "proposed")
	insertOldFact(t, fromDir, "f3", "p2", "d2", "skills", `{"key":"sql","name":"SQL","level":"Expert"}`, "q3", "proposed")

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	ps := readNewProposals(t, targetDir)
	if got := proposalValue(ps, "targetRole"); len(got) != 1 || got[0] != "Platform Engineer" {
		t.Errorf("target roles: got %q", got)
	}
	if got := proposalValue(ps, "skill"); len(got) != 1 || got[0] != "SQL (Expert)" {
		t.Errorf("skills of the other profile: got %q", got)
	}
	if !strings.Contains(report, "dropped (target role of a profile that is not the active one): 1") {
		t.Errorf("report: %s", report)
	}
}

func TestImport_ProposalOfDeletedOrUnknownDocumentNotCarried(t *testing.T) {
	fromDir := setupProposalFixture(t)
	insertOldDocument(t, fromDir, oldDoc{id: "dx", profile: "p1", kind: "md", name: "gone.md", state: "deleted"})
	insertOldFact(t, fromDir, "f1", "p1", "dx", "skills", `{"key":"go","name":"Go","level":"Proficient"}`, "q1", "proposed")
	insertOldFact(t, fromDir, "f2", "p1", "", "skills", `{"key":"rust","name":"Rust","level":"Expert"}`, "q2", "proposed")
	insertOldFact(t, fromDir, "f3", "p1", "d1", "skills", `not json`, "q3", "proposed")

	targetDir := t.TempDir()
	report := runImport(t, fromDir, targetDir)

	if ps := readNewProposals(t, targetDir); len(ps) != 0 {
		t.Errorf("proposals: got %+v, want none", ps)
	}
	if !strings.Contains(report, "dropped (its document was not carried): 2") ||
		!strings.Contains(report, "dropped (value could not be read): 1") {
		t.Errorf("report: %s", report)
	}
}
