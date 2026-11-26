package catalog_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/listing"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func fixedNow() func() time.Time {
	t := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func posting(rawURL, title, company string) listing.Posting {
	return listing.Posting{
		URL:       rawURL,
		Title:     title,
		Company:   company,
		Text:      "Full text of the vacancy.",
		TextState: listing.TextFull,
	}
}

func TestUpsert_NewVacancy(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())

	id, outcome, err := cat.Upsert(context.Background(), posting("https://example.com/jobs/1", "Engineer", "Acme"), "src1")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if outcome != catalog.OutcomeNew {
		t.Errorf("outcome: got %v, want OutcomeNew", outcome)
	}
	if id <= 0 {
		t.Errorf("id: got %d, want > 0", id)
	}
	var srcCount int
	if err := d.QueryRow(`SELECT count(*) FROM vacancy_source WHERE vacancy_id = ? AND source = ?`, id, "src1").Scan(&srcCount); err != nil {
		t.Fatalf("source query: %v", err)
	}
	if srcCount != 1 {
		t.Errorf("source rows: got %d, want 1", srcCount)
	}

	p2 := posting("https://example.com/jobs/3", "Designer", "")
	id2, _, err := cat.Upsert(context.Background(), p2, "src")
	if err != nil {
		t.Fatalf("empty company upsert: %v", err)
	}
	var company string
	if err := d.QueryRow(`SELECT company FROM vacancy WHERE id = ?`, id2).Scan(&company); err != nil {
		t.Fatalf("company query: %v", err)
	}
	if company != "" {
		t.Errorf("company: got %q, want %q (stored as empty when unknown)", company, "")
	}
}

func TestUpsert_MatchedByAddress(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())

	p0 := posting("https://example.com/jobs/10", "Engineer", "Acme")
	cat.Upsert(context.Background(), p0, "src")
	_, outcome, err := cat.Upsert(context.Background(), p0, "src")
	if err != nil {
		t.Fatalf("unchanged upsert: %v", err)
	}
	if outcome != catalog.OutcomeUnchanged {
		t.Errorf("unchanged outcome: got %v, want OutcomeUnchanged", outcome)
	}

	p1 := posting("https://example.com/jobs/12", "Engineer", "Acme")
	id1, _, _ := cat.Upsert(context.Background(), p1, "src")
	p1.Text = "Different content here."
	_, out1, err := cat.Upsert(context.Background(), p1, "src")
	if err != nil {
		t.Fatalf("text update upsert: %v", err)
	}
	if out1 != catalog.OutcomeUpdated {
		t.Errorf("text update outcome: got %v, want OutcomeUpdated", out1)
	}
	var version int
	if d.QueryRow(`SELECT version FROM vacancy WHERE id = ?`, id1).Scan(&version); version != 2 {
		t.Errorf("version: got %d, want 2", version)
	}

	p2 := posting("https://example.com/jobs/14", "Old Title", "Acme")
	p2.Fields = map[string]string{"salary": "100k", "type": "full-time"}
	id2, _, _ := cat.Upsert(context.Background(), p2, "src")
	p2.Title = "New Title"
	p2.Company = ""
	p2.Fields = map[string]string{"salary": "120k", "remote": "yes"}
	cat.Upsert(context.Background(), p2, "src")
	var title, company, fields string
	d.QueryRow(`SELECT title, company, source_fields FROM vacancy WHERE id = ?`, id2).
		Scan(&title, &company, &fields)
	if title != "New Title" {
		t.Errorf("title: got %q, want New Title", title)
	}
	if company != "Acme" {
		t.Errorf("company: got %q, want Acme (empty company should not replace named)", company)
	}
	if !strings.Contains(fields, `"salary":"120k"`) || !strings.Contains(fields, `"type":"full-time"`) || !strings.Contains(fields, `"remote":"yes"`) {
		t.Errorf("fields merged incorrectly: %s", fields)
	}

	t0 := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2024, 2, 1, 9, 0, 0, 0, time.UTC)
	call := 0
	cat2 := catalog.New(d, func() time.Time {
		call++
		if call == 1 {
			return t0
		}
		return t1
	})
	p3 := posting("https://example.com/jobs/11", "Engineer", "Acme")
	id3, _, _ := cat2.Upsert(context.Background(), p3, "src")
	cat2.Upsert(context.Background(), p3, "src")
	var lastSeen string
	if err := d.QueryRow(`SELECT last_seen FROM vacancy WHERE id = ?`, id3).Scan(&lastSeen); err != nil {
		t.Fatalf("last_seen query: %v", err)
	}
	if lastSeen != t1.Format(time.RFC3339) {
		t.Errorf("last_seen: got %q, want %q", lastSeen, t1.Format(time.RFC3339))
	}
}

func TestUpsert_MatchedByApplyAddress(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())

	applyURL := "https://jobs.lever.co/acme/550e8400-e29b-41d4-a716-446655440000"

	p1 := posting("https://acme.com/careers/engineer", "Engineer", "Acme")
	p1.ApplyURL = applyURL
	id1, _, _ := cat.Upsert(context.Background(), p1, "src1")

	p2 := posting("https://acme.jobs/engineer", "Engineer", "Acme")
	p2.ApplyURL = applyURL
	id2, outcome, err := cat.Upsert(context.Background(), p2, "src2")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id2 != id1 {
		t.Errorf("id: got %d, want %d (same vacancy)", id2, id1)
	}
	if outcome == catalog.OutcomeNew {
		t.Error("outcome: should not be OutcomeNew")
	}
	ats := "https://jobs.ashbyhq.com/acme/550e8400-e29b-41d4-a716-446655440000"
	cat2 := catalog.New(openTestDB(t), fixedNow())
	agg := posting("https://aggregator.example/jobs/9", "Platform Engineer", "Acme")
	agg.ApplyURL = ats + "/application"
	atsID1, _, _ := cat2.Upsert(context.Background(), agg, "agg")
	board := posting(ats, "Platform Engineer", "acme")
	atsID2, atsOut, atsErr := cat2.Upsert(context.Background(), board, "board")
	if atsErr != nil || atsID2 != atsID1 || atsOut == catalog.OutcomeNew {
		t.Fatalf("board posting: id %d (want %d), outcome %v, err %v", atsID2, atsID1, atsOut, atsErr)
	}
}

func TestUpsert_MatchedByIdentityOnAnotherHost(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())

	p1 := posting("https://acme.com/jobs/engineer", "Software Engineer", "Acme Corp")
	id1, _, _ := cat.Upsert(context.Background(), p1, "src1")

	p2 := posting("https://boards.greenhouse.io/acme/jobs/9999", "Software Engineer", "Acme Corp")
	id2, outcome, err := cat.Upsert(context.Background(), p2, "src2")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id2 != id1 {
		t.Errorf("id: got %d, want %d (same vacancy)", id2, id1)
	}
	if outcome == catalog.OutcomeNew {
		t.Error("outcome: should not be OutcomeNew")
	}
}

func TestUpsert_NoMatchCases(t *testing.T) {
	type spec struct{ url, title, company, applyURL string }
	cases := []struct {
		name   string
		p1, p2 spec
	}{
		{
			"same host same identity",
			spec{"https://acme.com/jobs/1", "Software Engineer", "Acme Corp", ""},
			spec{"https://acme.com/jobs/2", "Software Engineer", "Acme Corp", ""},
		},
		{
			"unknown company",
			spec{"https://acme.com/jobs/nm1", "Data Analyst", "", ""},
			spec{"https://other.com/jobs/nm1", "Data Analyst", "", ""},
		},
		{
			"conflicting apply URLs",
			spec{"https://acme.com/jobs/nm2", "Engineer", "Acme Corp", "https://jobs.lever.co/acme/550e8400-e29b-41d4-a716-446655440000"},
			spec{"https://board.com/acme/nm2", "Engineer", "Acme Corp", "https://jobs.lever.co/acme/660e8400-e29b-41d4-a716-446655440001"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			cat := catalog.New(d, fixedNow())
			p1 := posting(tc.p1.url, tc.p1.title, tc.p1.company)
			p1.ApplyURL = tc.p1.applyURL
			id1, _, _ := cat.Upsert(context.Background(), p1, "src")
			p2 := posting(tc.p2.url, tc.p2.title, tc.p2.company)
			p2.ApplyURL = tc.p2.applyURL
			id2, outcome, err := cat.Upsert(context.Background(), p2, "src")
			if err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if id2 == id1 {
				t.Error("should not match")
			}
			if outcome != catalog.OutcomeNew {
				t.Errorf("outcome: got %v, want OutcomeNew", outcome)
			}
		})
	}
}

func TestUpsert_TextReplacement(t *testing.T) {
	cases := []struct {
		url      string
		initText string
		init     listing.TextState
		newText  string
		new      listing.TextState
		wantOut  catalog.Outcome
	}{
		{"u20", "Full.", listing.TextFull, "Brief.", listing.TextSummary, catalog.OutcomeUnchanged},
		{"u21", "Full.", listing.TextFull, "Block.", listing.TextBlocked, catalog.OutcomeUnchanged},
		{"u22", "", listing.TextRemoved, "Full text.", listing.TextFull, catalog.OutcomeUpdated},
		{"u23", "", listing.TextNotFetched, "Full text.", listing.TextFull, catalog.OutcomeUpdated},
	}
	for _, tc := range cases {
		d := openTestDB(t)
		cat := catalog.New(d, fixedNow())
		p := posting("https://example.com/jobs/"+tc.url, "Engineer", "Acme")
		p.Text = tc.initText
		p.TextState = tc.init
		id, _, _ := cat.Upsert(context.Background(), p, "src")
		p.Text = tc.newText
		p.TextState = tc.new
		_, out, _ := cat.Upsert(context.Background(), p, "src")
		if out != tc.wantOut {
			t.Errorf("%s: outcome %v, want %v", tc.url, out, tc.wantOut)
		}
		var state string
		if d.QueryRow(`SELECT text_state FROM vacancy WHERE id = ?`, id).Scan(&state); state != "full" {
			t.Errorf("%s: text_state %q, want full", tc.url, state)
		}
	}
	d2 := openTestDB(t)
	pw := posting("https://example.com/jobs/u31", "Analyst", "Acme")
	pw.Text, pw.TextState = "<p>Headquarters:</p><p>Remote</p>", listing.TextFull
	idw, _, _ := catalog.New(d2, fixedNow()).Upsert(context.Background(), pw, "src")
	pw.Text = "<p>Headquarters: Remote</p>"
	if _, out, _ := catalog.New(d2, fixedNow()).Upsert(context.Background(), pw, "src"); out != catalog.OutcomeUnchanged {
		t.Fatalf("whitespace-only change: outcome %v, want Unchanged", out)
	}
	var v int
	if d2.QueryRow(`SELECT version FROM vacancy WHERE id = ?`, idw).Scan(&v); v != 1 {
		t.Fatalf("whitespace-only change: version %d, want 1", v)
	}
}

func TestUpsert_PublicationDateRules(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	past := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	future := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	p1 := posting("https://example.com/jobs/30", "Engineer", "Acme")
	p1.Published = past
	p1.PublishedBasis = "datePosted"
	id1, _, _ := cat.Upsert(context.Background(), p1, "src")
	var pubAt, pubBasis string
	if d.QueryRow(`SELECT published_at, published_basis FROM vacancy WHERE id = ?`, id1).Scan(&pubAt, &pubBasis); pubAt == "" || pubBasis != "datePosted" {
		t.Errorf("published_at=%q basis=%q", pubAt, pubBasis)
	}
	p1.Published = time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC)
	p1.PublishedBasis = "pubDate"
	cat.Upsert(context.Background(), p1, "src")
	var pubAt2 string
	if d.QueryRow(`SELECT published_at FROM vacancy WHERE id = ?`, id1).Scan(&pubAt2); pubAt2 != pubAt {
		t.Errorf("date replaced: %q → %q", pubAt, pubAt2)
	}
	p2 := posting("https://example.com/jobs/32", "Engineer", "Acme")
	p2.Published = future
	id2, _, _ := cat.Upsert(context.Background(), p2, "src")
	var pubAt3 string
	if d.QueryRow(`SELECT published_at FROM vacancy WHERE id = ?`, id2).Scan(&pubAt3); pubAt3 != "" {
		t.Errorf("future date stored: %q", pubAt3)
	}
	p3 := posting("https://example.com/jobs/33", "Engineer", "Acme")
	p3.Expires = time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	id3, _, _ := cat.Upsert(context.Background(), p3, "src")
	p3.Expires = time.Time{}
	cat.Upsert(context.Background(), p3, "src")
	var exp string
	if d.QueryRow(`SELECT expires_at FROM vacancy WHERE id = ?`, id3).Scan(&exp); exp == "" {
		t.Error("expires_at cleared when posting had no expiry")
	}
}

func TestUpsert_TombstonedURLIsRejected(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())

	canonURL := "https://example.com/jobs/60"
	d.Exec(`INSERT INTO tombstone (key) VALUES (?)`, "url:"+canonURL)
	_, outcome, err := cat.Upsert(context.Background(), posting(canonURL, "Engineer", "Acme"), "src")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if outcome != catalog.OutcomeTombstoned {
		t.Errorf("url tombstone: got %v, want OutcomeTombstoned", outcome)
	}
	var count int
	if d.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&count); count != 0 {
		t.Errorf("vacancy count: got %d, want 0", count)
	}

	rawURL := "https://Example.COM/jobs/80"
	h := sha256.Sum256([]byte("url:" + rawURL))
	tombKey := "legacy:" + hex.EncodeToString(h[:])
	d.Exec(`INSERT INTO tombstone (key) VALUES (?)`, tombKey)
	_, out2, err := cat.Upsert(context.Background(), posting(rawURL, "Engineer", "Acme"), "src")
	if err != nil {
		t.Fatalf("legacy upsert: %v", err)
	}
	if out2 != catalog.OutcomeTombstoned {
		t.Errorf("legacy tombstone: got %v, want OutcomeTombstoned", out2)
	}
}

func TestUpsert_ApplyURLRules(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	p1 := posting("https://example.com/jobs/70", "Engineer", "Acme")
	id1, _, _ := cat.Upsert(context.Background(), p1, "src")
	p1.ApplyURL = "https://jobs.lever.co/acme/550e8400-e29b-41d4-a716-446655440000"
	cat.Upsert(context.Background(), p1, "src")
	var got1 string
	if d.QueryRow(`SELECT apply_url FROM vacancy WHERE id = ?`, id1).Scan(&got1); got1 == "" {
		t.Error("apply_url: should be set after update")
	}
	orig := "https://jobs.lever.co/acme/550e8400-e29b-41d4-a716-446655440000"
	p2 := posting("https://example.com/jobs/71", "Engineer", "Acme")
	p2.ApplyURL = orig
	id2, _, _ := cat.Upsert(context.Background(), p2, "src")
	p2.ApplyURL = "https://jobs.lever.co/acme/770e8400-e29b-41d4-a716-446655440002"
	cat.Upsert(context.Background(), p2, "src")
	var got2 string
	if d.QueryRow(`SELECT apply_url FROM vacancy WHERE id = ?`, id2).Scan(&got2); got2 != orig {
		t.Errorf("apply_url overwritten: got %q, want %q", got2, orig)
	}
	p3 := posting("https://example.com/jobs/72", "Designer", "Acme")
	p3.ApplyURL = "https://apply.acmecorp.example/"
	id3, _, _ := cat.Upsert(context.Background(), p3, "src")
	var got3 string
	if d.QueryRow(`SELECT apply_url FROM vacancy WHERE id = ?`, id3).Scan(&got3); got3 != "" {
		t.Errorf("apply_url kept for a non-ATS address: %q", got3)
	}
}

func TestUpsert_MergeVacancies(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())

	p1 := posting("https://acme.com/jobs/e1", "Site Reliability Engineer", "Acme Co")
	id1, out1, err := cat.Upsert(context.Background(), p1, "src1")
	if err != nil || out1 != catalog.OutcomeNew {
		t.Fatalf("first upsert: %v / %v", err, out1)
	}
	p2 := posting("https://acme.com/jobs/e2", "Site Reliability Engineer", "Acme Co")
	id2, out2, err := cat.Upsert(context.Background(), p2, "src2")
	if err != nil || out2 != catalog.OutcomeNew {
		t.Fatalf("second upsert: %v / %v", err, out2)
	}
	if id1 == id2 {
		t.Fatal("expected separate vacancies on same host")
	}
	keptID2, outcome2, err := cat.Upsert(context.Background(),
		posting("https://other.com/jobs/sre", "Site Reliability Engineer", "Acme Co"), "src3")
	if err != nil {
		t.Fatalf("2-way merge: %v", err)
	}
	if outcome2 != catalog.OutcomeMerged {
		t.Errorf("2-way outcome: got %v, want OutcomeMerged", outcome2)
	}
	wantKept := id1
	if id2 < id1 {
		wantKept = id2
	}
	if keptID2 != wantKept {
		t.Errorf("kept id: got %d, want %d (the lowest)", keptID2, wantKept)
	}

	for i, url := range []string{
		"https://zeta.com/jobs/t1",
		"https://zeta.com/jobs/t2",
		"https://zeta.com/jobs/t3",
	} {
		_, out, err := cat.Upsert(context.Background(),
			posting(url, "Platform Engineer", "Zeta Corp"), fmt.Sprintf("src%d", i+4))
		if err != nil || out != catalog.OutcomeNew {
			t.Fatalf("upsert %d: %v / %v", i+1, err, out)
		}
	}
	_, outcome3, err := cat.Upsert(context.Background(),
		posting("https://other.com/jobs/pe", "Platform Engineer", "Zeta Corp"), "src7")
	if err != nil {
		t.Fatalf("3-way merge: %v", err)
	}
	if outcome3 != catalog.OutcomeMerged {
		t.Errorf("3-way outcome: got %v, want OutcomeMerged", outcome3)
	}
	var count int
	d.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&count)
	if count != 2 {
		t.Errorf("vacancy count after 3-way merge: got %d, want 2", count)
	}
}

func TestUpsert_MergeTransfersBestText(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	lowP := listing.Posting{URL: "https://c.io/j1", Title: "AI Dev", Company: "C",
		Text: "Brief.", TextState: listing.TextSummary}
	idLow, _, _ := cat.Upsert(context.Background(), lowP, "s1")
	highP := listing.Posting{URL: "https://c.io/j2", Title: "AI Dev", Company: "C",
		Text: "Full text.", TextState: listing.TextFull}
	idHigh, _, _ := cat.Upsert(context.Background(), highP, "s2")
	if idHigh <= idLow {
		t.Fatal("expected idHigh > idLow")
	}
	trigger := listing.Posting{URL: "https://ai.c.io/j", Title: "AI Dev", Company: "C", TextState: listing.TextNotFetched}
	_, out, _ := cat.Upsert(context.Background(), trigger, "s3")
	if out != catalog.OutcomeMerged {
		t.Fatalf("merge outcome: %v", out)
	}
	var state string
	var ver int
	if d.QueryRow(`SELECT text_state, version FROM vacancy WHERE id = ?`, idLow).Scan(&state, &ver); state != "full" || ver != 2 {
		t.Errorf("text_state=%q ver=%d, want full/2", state, ver)
	}
}

func TestUpsert_MergeAliasRowsAndSources(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())

	p1 := posting("https://acme.com/jobs/a1", "Data Engineer", "Gamma Ltd")
	id1, _, _ := cat.Upsert(context.Background(), p1, "src1")
	p2 := posting("https://acme.com/jobs/a2", "Data Engineer", "Gamma Ltd")
	id2, _, _ := cat.Upsert(context.Background(), p2, "src2")

	var oldAlias int64 = 9999
	higherID := id2
	if id1 > id2 {
		higherID = id1
	}
	d.Exec(`INSERT INTO vacancy_alias (old_id, new_id) VALUES (?, ?)`, oldAlias, higherID)

	p3 := posting("https://gamma-jobs.com/de", "Data Engineer", "Gamma Ltd")
	keptID, outcome, err := cat.Upsert(context.Background(), p3, "src3")
	if err != nil {
		t.Fatalf("merge upsert: %v", err)
	}
	if outcome != catalog.OutcomeMerged {
		t.Errorf("outcome: got %v, want OutcomeMerged", outcome)
	}

	mergedID := id2
	if id2 == keptID {
		mergedID = id1
	}
	var newID int64
	if err := d.QueryRow(`SELECT new_id FROM vacancy_alias WHERE old_id = ?`, mergedID).Scan(&newID); err != nil {
		t.Fatalf("alias row not found for merged id %d: %v", mergedID, err)
	}
	if newID != keptID {
		t.Errorf("alias new_id: got %d, want %d", newID, keptID)
	}

	var newTarget int64
	if d.QueryRow(`SELECT new_id FROM vacancy_alias WHERE old_id = ?`, oldAlias).Scan(&newTarget); newTarget != keptID {
		t.Errorf("old alias repointed: got %d, want %d", newTarget, keptID)
	}

	for _, mid := range []int64{id1, id2} {
		if mid == keptID {
			continue
		}
		var orphaned int
		if d.QueryRow(`SELECT count(*) FROM vacancy_source WHERE vacancy_id = ?`, mid).Scan(&orphaned); orphaned != 0 {
			t.Errorf("source rows for merged id %d: got %d, want 0", mid, orphaned)
		}
	}
	var kept int
	if d.QueryRow(`SELECT count(*) FROM vacancy_source WHERE vacancy_id = ?`, keptID).Scan(&kept); kept < 2 {
		t.Errorf("source rows for kept id %d: got %d, want >= 2", keptID, kept)
	}
}

func TestUpsert_CrossATSApplyRules(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	p1 := posting("https://acme.com/jobs/engineer", "Platform Engineer", "Acme Corp")
	p1.ApplyURL = "https://jobs.ashbyhq.com/acme/550e8400-e29b-41d4-a716-446655440000"
	id1, _, _ := cat.Upsert(context.Background(), p1, "src1")
	p2 := posting("https://anotherjobs.com/engineer", "Platform Engineer", "Acme Corp")
	p2.ApplyURL = "https://boards.greenhouse.io/acme/jobs/9999"
	id2, outcome, err := cat.Upsert(context.Background(), p2, "src2")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id2 != id1 {
		t.Errorf("id: got %d, want %d (should match across ATS systems)", id2, id1)
	}
	if outcome == catalog.OutcomeNew {
		t.Error("outcome: should not be OutcomeNew for a cross-ATS match")
	}
	var kept string
	if d.QueryRow(`SELECT apply_url FROM vacancy WHERE id = ?`, id1).Scan(&kept); kept == "" || !strings.Contains(kept, "ashbyhq.com") {
		t.Errorf("apply_url: got %q, want the Ashby address (kept on match)", kept)
	}

	d2 := openTestDB(t)
	cat2 := catalog.New(d2, fixedNow())
	p12 := posting("https://acme.com/jobs/sre", "Site Reliability Engineer", "Acme Corp")
	p12.ApplyURL = "https://boards.greenhouse.io/acme/jobs/1111"
	id12, _, _ := cat2.Upsert(context.Background(), p12, "src1")
	p22 := posting("https://anotherjobs.com/sre", "Site Reliability Engineer", "Acme Corp")
	p22.ApplyURL = "https://boards.greenhouse.io/acme/jobs/2222"
	id22, outcome2, err2 := cat2.Upsert(context.Background(), p22, "src2")
	if err2 != nil {
		t.Fatalf("second upsert (same-ATS): %v", err2)
	}
	if id22 == id12 {
		t.Error("should not match: two different concrete vacancies on the same ATS")
	}
	if outcome2 != catalog.OutcomeNew {
		t.Errorf("outcome: got %v, want OutcomeNew", outcome2)
	}
}

func TestUpsert_SameEuropeanGreenhouseVacancyFromTwoSourcesIsOne(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	p1 := posting("https://acme.com/jobs/a", "Platform Engineer", "Acme Corp")
	p1.ApplyURL = "https://boards.eu.greenhouse.io/acme/jobs/77/apply"
	id1, _, _ := cat.Upsert(context.Background(), p1, "src1")
	p2 := posting("https://anotherjobs.com/b", "Platform Developer", "Other Name")
	p2.ApplyURL = "https://boards.eu.greenhouse.io/acme/jobs/77"
	if id2, _, err := cat.Upsert(context.Background(), p2, "src2"); err != nil || id2 != id1 {
		t.Errorf("second upsert: id %d (want %d), err %v", id2, id1, err)
	}
	d.Exec(`INSERT INTO exclusion (kind,value,norm,profile_id,created) VALUES ('company','acme','acme',0,datetime('now'))`)
	if ok, _ := catalog.Excluded(context.Background(), d, "Role", "Unrelated", p1.ApplyURL); !ok {
		t.Error("company exclusion does not match the apply address on a European board")
	}
}

func TestUpsert_GuessedCompanyRules(t *testing.T) {
	for _, tc := range []struct{ init, next, want string }{
		{"Acme Corp", "acme-board", "Acme Corp"},
		{"", "acme-board", "acme-board"},
	} {
		d := openTestDB(t)
		cat := catalog.New(d, fixedNow())
		p := posting("https://acme.com/jobs/role", "Role", tc.init)
		id, _, _ := cat.Upsert(context.Background(), p, "src1")
		p.Company = tc.next
		p.CompanyGuessed = true
		cat.Upsert(context.Background(), p, "src2")
		var got string
		if d.QueryRow(`SELECT company FROM vacancy WHERE id = ?`, id).Scan(&got); got != tc.want {
			t.Errorf("init=%q next=%q: company = %q, want %q", tc.init, tc.next, got, tc.want)
		}
	}
	dg := openTestDB(t)
	catg := catalog.New(dg, fixedNow())
	pg := posting("https://newboard.com/jobs/dev", "Developer", "newboard-slug")
	pg.CompanyGuessed = true
	idg, outcome, err := catg.Upsert(context.Background(), pg, "src1")
	if err != nil || outcome != catalog.OutcomeNew {
		t.Fatalf("upsert: %v / %v", err, outcome)
	}
	var company string
	if dg.QueryRow(`SELECT company FROM vacancy WHERE id = ?`, idg).Scan(&company); company != "newboard-slug" {
		t.Errorf("company: got %q, want newboard-slug (guessed company kept for new vacancy)", company)
	}
}

func TestKnown(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	ctx := context.Background()
	d.Exec(`INSERT INTO tombstone (key) VALUES ('url:https://example.com/jobs/t1')`)
	if k, err := cat.Known(ctx, listing.Posting{URL: "https://example.com/jobs/t1", Title: "X", Company: "Y"}); err != nil || !k.Removed {
		t.Fatalf("tombstoned: %+v err=%v", k, err)
	}
	if k, err := cat.Known(ctx, listing.Posting{URL: "https://example.com/jobs/new", Title: "N", Company: "C"}); err != nil || !k.New || k.HasText {
		t.Fatalf("new: %+v err=%v", k, err)
	}
	p := listing.Posting{URL: "https://example.com/jobs/e1", Title: "Dev", Company: "Co", TextState: listing.TextNotFetched}
	cat.Upsert(ctx, p, "s")
	if k, err := cat.Known(ctx, p); err != nil || k.New || k.HasText || !k.Requested.IsZero() {
		t.Fatalf("existing no text: %+v err=%v", k, err)
	}
	p2 := posting("https://example.com/jobs/e2", "Dev2", "Co")
	cat.Upsert(ctx, p2, "s")
	if k, err := cat.Known(ctx, p2); err != nil || !k.HasText {
		t.Fatalf("existing with text: %+v err=%v", k, err)
	}
	p3 := listing.Posting{URL: "https://example.com/jobs/e3", Title: "Dev3", Company: "Co",
		TextState: listing.TextNotFetched, Requested: true}
	cat.Upsert(ctx, p3, "s")
	if k, _ := cat.Known(ctx, p3); k.Requested.IsZero() {
		t.Fatal("want non-zero Requested after Upsert with Requested=true")
	}
}

func TestUpsert_BlockedStateStoredWithoutText(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	p := listing.Posting{URL: "https://example.com/jobs/b1", Title: "Eng", Company: "Co", TextState: listing.TextNotFetched}
	id, _, _ := cat.Upsert(context.Background(), p, "s")
	p.TextState = listing.TextBlocked
	cat.Upsert(context.Background(), p, "s")
	var state string
	if d.QueryRow(`SELECT text_state FROM vacancy WHERE id = ?`, id).Scan(&state); state != "blocked" {
		t.Errorf("text_state: got %q, want blocked", state)
	}
}

func TestUpsert_EmptyBodyNotFetchedDoesNotError(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	const vacURL = "https://board.example.com/jobs/imported-42"
	_, err := d.Exec(`
		INSERT INTO vacancy (identity, title, company, url, text_state, body,
		                     first_seen, last_seen, version, content_hash)
		VALUES ('Imported Co|Backend Dev', 'Backend Dev', 'Imported Co',
		        ?, 'not_fetched', X'',
		        '2024-01-01', '2024-01-01', 1, '')`, vacURL)
	if err != nil {
		t.Fatalf("fixture insert: %v", err)
	}
	_, err = d.Exec(`INSERT INTO vacancy_source (vacancy_id, source, url, seen_at)
		VALUES (last_insert_rowid(), 'src', ?, '2024-01-01')`, vacURL)
	if err != nil {
		t.Fatalf("fixture source insert: %v", err)
	}
	p := listing.Posting{URL: vacURL, Title: "Backend Dev", Company: "Imported Co",
		TextState: listing.TextNotFetched}
	_, _, err = cat.Upsert(context.Background(), p, "src")
	if err != nil {
		t.Fatalf("Upsert on empty-body vacancy: %v", err)
	}
	var count int
	var bodyIsNull bool
	if d.QueryRow(`SELECT count(*), body IS NULL FROM vacancy WHERE url = ?`, vacURL).Scan(&count, &bodyIsNull); count != 1 {
		t.Errorf("vacancy count = %d, want 1", count)
	}
	if bodyIsNull {
		t.Errorf("body IS NULL after upsert, want empty blob")
	}
}

func TestUpsert_MergePreservesBestDecision(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	id1, _, _ := cat.Upsert(context.Background(), posting("https://a.example.com/jobs/1", "Engineer", "Acme"), "src")
	id2, _, _ := cat.Upsert(context.Background(), posting("https://b.example.com/jobs/2", "Engineer", "Acme"), "src")
	now := "2024-01-15T12:00:00Z"
	d.Exec(`INSERT INTO profile (id, data) VALUES (1, '{"name":"P"}') ON CONFLICT DO NOTHING`)
	d.Exec(`INSERT OR REPLACE INTO decision (profile,vacancy,stage,reason,decided_at) VALUES (1,?,?,?,?)`, id1, "shortlist", "", now)
	d.Exec(`INSERT OR REPLACE INTO decision (profile,vacancy,stage,reason,decided_at) VALUES (1,?,?,?,?)`, id2, "applied", "", now)

	keptID, _, _ := cat.Upsert(context.Background(),
		listing.Posting{URL: "https://c.example.com/jobs/3", Title: "Engineer", Company: "Acme", TextState: listing.TextNotFetched}, "src")
	var stage string
	if d.QueryRow(`SELECT stage FROM decision WHERE vacancy=? AND profile=1`, keptID).Scan(&stage); stage != "applied" {
		t.Errorf("merged decision stage = %q, want applied", stage)
	}
	var vcount int
	if d.QueryRow(`SELECT count(*) FROM vacancy`).Scan(&vcount); vcount != 1 {
		t.Errorf("vacancy count = %d, want 1 after merge", vcount)
	}
}

func TestUpsert_DeleteTombstonesAddress(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	id, _, _ := cat.Upsert(context.Background(), posting("https://example.com/jobs/del", "Engineer", "Acme"), "src")
	d.Exec(`INSERT INTO profile (id, data) VALUES (1, '{"name":"P"}') ON CONFLICT DO NOTHING`)
	now := "2024-01-15T12:00:00Z"
	d.Exec(`INSERT OR REPLACE INTO decision (profile,vacancy,stage,reason,decided_at) VALUES (1,?,?,?,?)`, id, "shortlist", "", now)
	if err := cat.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var vcount int
	if d.QueryRow(`SELECT count(*) FROM vacancy WHERE id=?`, id).Scan(&vcount); vcount != 0 {
		t.Errorf("vacancy still present after Delete")
	}
	_, out, err := cat.Upsert(context.Background(), posting("https://example.com/jobs/del", "Engineer", "Acme"), "src")
	if err != nil || out != catalog.OutcomeTombstoned {
		t.Errorf("re-upsert after Delete: outcome=%v err=%v, want Tombstoned", out, err)
	}
}

func TestUpsert_FTSPreservedOnUnchangedUpsert(t *testing.T) {
	d := openTestDB(t)
	cat := catalog.New(d, fixedNow())
	p := posting("https://example.com/jobs/fts1", "Site Reliability Engineer", "Ops")
	id, _, _ := cat.Upsert(context.Background(), p, "src")

	_, out, _ := cat.Upsert(context.Background(), p, "src")
	if out != catalog.OutcomeUnchanged {
		t.Fatalf("second upsert: outcome %v, want Unchanged", out)
	}
	var n int
	if d.QueryRow(`SELECT count(*) FROM vacancy_fts WHERE rowid=?`, id).Scan(&n); n != 1 {
		t.Errorf("FTS row count after unchanged upsert: got %d, want 1", n)
	}
}
