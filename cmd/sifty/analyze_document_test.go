package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func pasteAnalyzeDoc(t *testing.T, base, name, text string) (map[string]any, int) {
	t.Helper()
	return apiSend(t, http.MethodPost, base, "/api/documents", map[string]any{"name": name, "text": text})
}

func fakeProposalReply(facts []map[string]any) string {
	out := map[string]any{"facts": facts}
	b, _ := json.Marshal(out)
	return string(b)
}

func emptyProposalReply() string {
	return fakeProposalReply(nil)
}

type pauseOnCall4 struct {
	*models.FakeModel
	n       atomic.Int32
	done    atomic.Bool
	blocked chan struct{}
}

func (p *pauseOnCall4) Complete(ctx context.Context, r models.Request) (models.Response, error) {
	if !p.done.Load() && p.n.Add(1) == 4 {
		close(p.blocked)
		<-ctx.Done()
		p.done.Store(true)
		return models.Response{}, ctx.Err()
	}
	return p.FakeModel.Complete(ctx, r)
}

type analyzeFix struct {
	d    *db.DB
	r    *runner.Runner
	fm   *models.FakeModel
	base string
}

func newAnalyzeFix(t *testing.T) analyzeFix {
	t.Helper()
	return newAnalyzeFixWithPDF(t, candidate.ProgramText{Program: filepath.Join(t.TempDir(), "absent")})
}

func newAnalyzeFixWithPDF(t *testing.T, pdfText candidate.TextSource) analyzeFix {
	t.Helper()
	d := openTestDB(t)
	r := runner.New(d, nil, time.Now, slog.Default())
	fm := &models.FakeModel{}
	registerKindsWith(t, r, d, fm)
	base := newRouteServerWithReaders(t, d, r, candidate.Readers{PDF: pdfText})
	return analyzeFix{d: d, r: r, fm: fm, base: base}
}

func TestAnalyzeQueuesJobAndDocumentShowsAnalysing(t *testing.T) {
	fix := newAnalyzeFix(t)
	fix.fm.AddReply(models.Response{Text: emptyProposalReply()})

	out, status := pasteAnalyzeDoc(t, fix.base, "My CV", "Experienced Go developer with five years of backend experience.")
	if status != http.StatusOK {
		t.Fatalf("paste doc: status %d body: %v", status, out)
	}
	docID := out["id"].(float64)

	analyzeOut, status := apiSend(t, http.MethodPost, fix.base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)
	if status != http.StatusOK {
		t.Fatalf("POST analyze: status %d body: %v", status, analyzeOut)
	}
	if analyzeOut["state"] != "analysing" {
		t.Errorf("after analyze: state = %v, want analysing", analyzeOut["state"])
	}

	var queued int
	fix.d.QueryRow(`SELECT count(*) FROM job WHERE kind='analyze_document' AND state IN ('queued','running')`).Scan(&queued)
	if queued == 0 {
		t.Error("analyze_document job not queued")
	}
}

func TestAnalyzeJobSavesFactsWithQuotes(t *testing.T) {
	fix := newAnalyzeFix(t)
	docText := "Proficient in Go and Python. English is my native language. I am seeking a Backend Engineer role."
	fix.fm.AddReply(models.Response{Text: fakeProposalReply([]map[string]any{
		{"field": "skill", "value": "Go", "quote": "Proficient in Go and Python."},
		{"field": "language", "value": "English (Native)", "quote": "English is my native language."},
		{"field": "targetRole", "value": "Backend Engineer", "quote": "seeking a Backend Engineer role."},
	})})

	base := fix.base
	out, _ := pasteAnalyzeDoc(t, base, "cv.txt", docText)
	docID := out["id"].(float64)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	waitForDocState(t, base, docID, "analysed", 5*time.Second)

	listOut, status := apiGet(t, base, "/api/proposals")
	if status != http.StatusOK {
		t.Fatalf("GET /api/proposals: status %d", status)
	}
	proposals, _ := listOut["proposals"].([]any)
	if len(proposals) != 3 {
		t.Fatalf("proposals count = %d, want 3", len(proposals))
	}
	fields := map[string]bool{}
	for _, p := range proposals {
		pm, _ := p.(map[string]any)
		fields[pm["field"].(string)] = true
		if pm["quote"] == nil || pm["quote"] == "" {
			t.Errorf("proposal has empty quote: %v", pm)
		}
	}
	for _, f := range []string{"skill", "language", "targetRole"} {
		if !fields[f] {
			t.Errorf("missing proposal for field %q", f)
		}
	}
}

func TestAnalyzeJobDropsFactWithMismatchedQuote(t *testing.T) {
	fix := newAnalyzeFix(t)
	docText := "Expert in Rust and C++. Looking for systems programming work."
	fix.fm.AddReply(models.Response{Text: fakeProposalReply([]map[string]any{
		{"field": "skill", "value": "Rust", "quote": "Expert in Rust and C++."},
		{"field": "skill", "value": "Java", "quote": "This quote does not appear in the document text at all."},
	})})

	base := fix.base
	out, _ := pasteAnalyzeDoc(t, base, "skills.txt", docText)
	docID := out["id"].(float64)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	waitForDocState(t, base, docID, "analysed", 5*time.Second)

	listOut, _ := apiGet(t, base, "/api/proposals")
	proposals, _ := listOut["proposals"].([]any)
	if len(proposals) != 1 {
		t.Fatalf("proposals count = %d, want 1 (mismatched quote dropped)", len(proposals))
	}
	pm := proposals[0].(map[string]any)
	if pm["value"] != "Rust" {
		t.Errorf("kept proposal value = %v, want Rust", pm["value"])
	}
}

func TestAnalyzeJobCapAt100Facts(t *testing.T) {
	fix := newAnalyzeFix(t)

	var sb strings.Builder
	var facts []map[string]any
	for i := 0; i < 101; i++ {
		skill := fmt.Sprintf("SkillXYZ%d", i)
		line := fmt.Sprintf("Experience with %s is required. ", skill)
		sb.WriteString(line)
		facts = append(facts, map[string]any{
			"field": "skill",
			"value": skill,
			"quote": strings.TrimSpace(line),
		})
	}
	docText := sb.String()
	fix.fm.AddReply(models.Response{Text: fakeProposalReply(facts)})

	base := fix.base
	out, _ := pasteAnalyzeDoc(t, base, "many.txt", docText)
	docID := out["id"].(float64)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	waitForDocState(t, base, docID, "analysed", 5*time.Second)

	listOut, _ := apiGet(t, base, "/api/proposals")
	proposals, _ := listOut["proposals"].([]any)
	if len(proposals) != 100 {
		t.Fatalf("proposals count = %d, want 100 (cap at 100)", len(proposals))
	}
}

func TestAnalyzeDuplicateFilterCoverage(t *testing.T) {
	cases := []struct {
		name     string
		meJSON   string
		profJSON string
		field    string
		value    string
		quote    string
		docText  string
	}{
		{
			name:   "skill already in me skills",
			meJSON: `{"skills":[{"name":"Go","level":"expert"}],"languages":[],"experience":[]}`,
			field:  "skill", value: "Go", quote: "Experienced Go developer.",
			docText: "Experienced Go developer.",
		},
		{
			name:   "skill in absent skills list",
			meJSON: `{"skills":[],"absentSkills":[{"name":"Rust"}],"languages":[],"experience":[]}`,
			field:  "skill", value: "Rust", quote: "Familiar with Rust too.",
			docText: "Familiar with Rust too.",
		},
		{
			name:   "language already in me languages",
			meJSON: `{"skills":[],"languages":[{"name":"French","level":"native"}],"experience":[]}`,
			field:  "language", value: "French (Native)", quote: "French is my first language.",
			docText: "French is my first language.",
		},
		{
			name:     "target role already in active profile",
			meJSON:   `{"skills":[],"languages":[],"experience":[]}`,
			profJSON: `{"name":"Main","targetRoles":["Backend Engineer"]}`,
			field:    "targetRole", value: "Backend Engineer", quote: "Seeking a Backend Engineer position.",
			docText: "Seeking a Backend Engineer position.",
		},
		{
			name:   "experience company already in me experience",
			meJSON: `{"skills":[],"languages":[],"experience":[{"company":"Acme Corp","title":"Engineer","start":"2020-01","current":true}]}`,
			field:  "experience", value: "Engineer at Acme Corp, 2020-present", quote: "Engineer at Acme Corp, 2020-present.",
			docText: "Engineer at Acme Corp, 2020-present.",
		},
		{
			name:   "same value proposed twice in one document",
			meJSON: `{"skills":[],"languages":[],"experience":[]}`,
			field:  "skill", value: "Docker",
			quote:   "Proficient in Docker.",
			docText: "Proficient in Docker. Also Docker certified.",
		},
		{
			name:   "value with internal extra space still matched",
			meJSON: `{"skills":[{"name":"Machine Learning","level":"proficient"}],"languages":[],"experience":[]}`,
			field:  "skill", value: "Machine  Learning", quote: "Machine  Learning expert.",
			docText: "Machine  Learning expert.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newAnalyzeFix(t)
			fix.d.Exec(`INSERT OR REPLACE INTO me (id, data) VALUES (1, ?)`, tc.meJSON)

			if tc.profJSON != "" {
				fix.d.Exec(`INSERT INTO profile (data) VALUES (?)`, tc.profJSON)
				var profID int64
				fix.d.QueryRow(`SELECT last_insert_rowid()`).Scan(&profID)
				fix.d.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('active_profile', ?)`,
					fmt.Sprintf("%d", profID))
			}

			facts := []map[string]any{
				{"field": tc.field, "value": tc.value, "quote": tc.quote},
			}
			if tc.name == "same value proposed twice in one document" {
				facts = append(facts, map[string]any{
					"field": tc.field, "value": tc.value, "quote": "Also Docker certified.",
				})
			}
			fix.fm.AddReply(models.Response{Text: fakeProposalReply(facts)})

			base := fix.base
			out, _ := pasteAnalyzeDoc(t, base, "cv.txt", tc.docText)
			docID := out["id"].(float64)

			apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go fix.r.Start(ctx)

			waitForDocState(t, base, docID, "analysed", 5*time.Second)

			listOut, _ := apiGet(t, base, "/api/proposals")
			proposals, _ := listOut["proposals"].([]any)
			want := 0
			if tc.name == "same value proposed twice in one document" {
				want = 1
			}
			if len(proposals) != want {
				t.Errorf("proposals = %d, want %d", len(proposals), want)
			}
		})
	}
}

func TestAnalyzeNoProviderSetsOpenSettingsError(t *testing.T) {
	fix := newAnalyzeFix(t)
	fix.fm.AddError(fmt.Errorf("%w for reading", models.ErrNoProvider))

	base := fix.base
	out, _ := pasteAnalyzeDoc(t, base, "cv.txt", "Some document text with real content here.")
	docID := out["id"].(float64)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	waitForDocState(t, base, docID, "error", 5*time.Second)

	body, _ := apiGet(t, base, "/api/documents")
	docs, _ := body["documents"].([]any)
	if len(docs) == 0 {
		t.Fatal("no documents")
	}
	doc := docs[0].(map[string]any)
	if doc["stateReason"] != "No model is chosen for reading. Open Settings." {
		t.Errorf("stateReason = %v, want no-provider message", doc["stateReason"])
	}
}

func TestAnalyzeSecondRunReplacesToReviewProposals(t *testing.T) {
	fix := newAnalyzeFix(t)
	docText := "Experienced in Go. Familiar with Python. Seeking a Backend role."
	fix.fm.AddReply(models.Response{Text: fakeProposalReply([]map[string]any{
		{"field": "skill", "value": "Go", "quote": "Experienced in Go."},
	})})
	fix.fm.AddReply(models.Response{Text: fakeProposalReply([]map[string]any{
		{"field": "skill", "value": "Python", "quote": "Familiar with Python."},
	})})

	base := fix.base
	out, _ := pasteAnalyzeDoc(t, base, "cv.txt", docText)
	docID := out["id"].(float64)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	waitForDocState(t, base, docID, "analysed", 5*time.Second)

	listOut, _ := apiGet(t, base, "/api/proposals")
	proposals, _ := listOut["proposals"].([]any)
	if len(proposals) != 1 || proposals[0].(map[string]any)["value"] != "Go" {
		t.Fatalf("first run: proposals = %v, want [Go]", proposals)
	}

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	for i := 0; i < 200; i++ {
		time.Sleep(5 * time.Millisecond)
		listOut, _ = apiGet(t, base, "/api/proposals")
		proposals, _ = listOut["proposals"].([]any)
		if len(proposals) == 1 {
			if p, ok := proposals[0].(map[string]any); ok && p["value"] == "Python" {
				return
			}
		}
	}
	t.Fatalf("second run did not replace proposals; got: %v", proposals)
}

func TestDeleteDocumentRemovesToReviewProposals(t *testing.T) {
	fix := newAnalyzeFix(t)
	fix.fm.AddReply(models.Response{Text: fakeProposalReply([]map[string]any{
		{"field": "skill", "value": "Rust", "quote": "Expert in Rust."},
	})})

	base := fix.base
	out, _ := pasteAnalyzeDoc(t, base, "cv.txt", "Expert in Rust. Working on systems software.")
	docID := out["id"].(float64)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	waitForDocState(t, base, docID, "analysed", 5*time.Second)

	listOut, _ := apiGet(t, base, "/api/proposals")
	proposals, _ := listOut["proposals"].([]any)
	if len(proposals) != 1 {
		t.Fatalf("before delete: %d proposals, want 1", len(proposals))
	}

	idStr := fmt.Sprintf("%.0f", docID)
	_, status := apiSend(t, http.MethodDelete, base, "/api/documents/"+idStr, nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE document: status %d", status)
	}

	listOut, _ = apiGet(t, base, "/api/proposals")
	proposals, _ = listOut["proposals"].([]any)
	if len(proposals) != 0 {
		t.Errorf("after delete: %d proposals remain, want 0", len(proposals))
	}
}

func TestPdfCannotBeAnalysedUntilTheProgramHasReadIt(t *testing.T) {
	program := filepath.Join(t.TempDir(), "pdftotext.sh")
	fix := newAnalyzeFixWithPDF(t, candidate.ProgramText{Program: program, Args: []string{"-", "-"}})
	fix.fm.AddReply(models.Response{Text: fakeProposalReply([]map[string]any{
		{"field": "skill", "value": "Go", "quote": "Expert Go developer."},
	})})

	resp, err := http.Post(fix.base+"/api/documents?name=cv.pdf", "application/pdf", strings.NewReader("%PDF-1.4 invented bytes"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	var doc map[string]any
	json.NewDecoder(resp.Body).Decode(&doc)
	resp.Body.Close()
	if doc["state"] != "unavailable" {
		t.Fatalf("state = %v, want unavailable", doc["state"])
	}
	path := fmt.Sprintf("/api/documents/%.0f", doc["id"])

	out, status := apiSend(t, http.MethodPost, fix.base, path+"/analyze", nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("analyze before reading: status %d, want 422; body: %v", status, out)
	}

	os.WriteFile(program, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'Expert Go developer.'"), 0755)
	out, status = apiSend(t, http.MethodPost, fix.base, path+"/refresh", nil)
	if status != http.StatusOK || out["state"] != "ready" {
		t.Fatalf("refresh: status %d, body: %v; want ready", status, out)
	}
	out, status = apiSend(t, http.MethodPost, fix.base, path+"/analyze", nil)
	if status != http.StatusOK || out["state"] != "analysing" {
		t.Fatalf("analyze after reading: status %d, body: %v; want analysing", status, out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)
	waitForDocState(t, fix.base, doc["id"].(float64), "analysed", 5*time.Second)
}

func TestPdfProgramIsStartedByNameWithAReducedEnvironment(t *testing.T) {
	uploadPDF := func(t *testing.T, port int) map[string]any {
		t.Helper()
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/api/documents?name=cv.pdf", port), "application/pdf", strings.NewReader("%PDF-1.4 invented bytes"))
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		defer resp.Body.Close()
		var doc map[string]any
		json.NewDecoder(resp.Body).Decode(&doc)
		return doc
	}

	t.Run("program on the path", func(t *testing.T) {
		bin, work := t.TempDir(), t.TempDir()
		script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %[1]s/args\n/usr/bin/env > %[1]s/env\n/bin/cat > /dev/null\nprintf 'Jane Roe'\n", work)
		if err := os.WriteFile(filepath.Join(bin, "pdftotext"), []byte(script), 0755); err != nil {
			t.Fatalf("write script: %v", err)
		}
		t.Setenv("PATH", bin)
		t.Setenv("SIFTY_TEST_SECRET", "invented-provider-key")
		port, cancel := startServer(t, t.TempDir(), true)
		defer cancel()

		doc := uploadPDF(t, port)
		if doc["state"] != "ready" || doc["charCount"] != float64(8) {
			t.Errorf("document = %v, want ready with the script's 8 characters", doc)
		}
		if args, _ := os.ReadFile(filepath.Join(work, "args")); string(args) != "-\n-\n" {
			t.Errorf("arguments = %q, want a dash twice", args)
		}
		env, _ := os.ReadFile(filepath.Join(work, "env"))
		if !strings.Contains(string(env), "PATH=") {
			t.Errorf("the program recorded no environment")
		}
		if strings.Contains(string(env), "SIFTY_TEST_SECRET") || strings.Contains(string(env), "invented-provider-key") {
			t.Errorf("the server's own environment reached the program")
		}
	})

	t.Run("program missing from the path", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		port, cancel := startServer(t, t.TempDir(), true)
		defer cancel()

		doc := uploadPDF(t, port)
		if reason, _ := doc["stateReason"].(string); doc["state"] != "unavailable" || !strings.Contains(reason, "poppler") {
			t.Errorf("document = %v, want unavailable with a reason that names poppler", doc)
		}
	})
}

func TestProposalListShowsDocumentName(t *testing.T) {
	fix := newAnalyzeFix(t)
	fix.fm.AddReply(models.Response{Text: fakeProposalReply([]map[string]any{
		{"field": "skill", "value": "Go", "quote": "Expert Go developer."},
	})})

	base := fix.base
	out, _ := pasteAnalyzeDoc(t, base, "My Resume.txt", "Expert Go developer. Five years experience.")
	docID := out["id"].(float64)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fix.r.Start(ctx)

	waitForDocState(t, base, docID, "analysed", 5*time.Second)

	listOut, _ := apiGet(t, base, "/api/proposals")
	proposals, _ := listOut["proposals"].([]any)
	if len(proposals) == 0 {
		t.Fatal("no proposals")
	}
	pm := proposals[0].(map[string]any)
	if pm["documentName"] != "My Resume.txt" {
		t.Errorf("documentName = %v, want My Resume.txt", pm["documentName"])
	}
}

func TestAnalyzeDelayedErrorKeepsDocumentAnalysing(t *testing.T) {
	retryAt := time.Now().Add(10 * time.Minute)
	cases := []struct {
		name string
		err  error
	}{
		{"WaitError", models.WaitError{RetryAfter: retryAt}},
		{"ErrBulkOnPaid", models.ErrBulkOnPaid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newAnalyzeFix(t)
			fix.fm.AddError(tc.err)

			base := fix.base
			out, _ := pasteAnalyzeDoc(t, base, "cv.txt", "Expert Go developer seeking backend roles.")
			docID := out["id"].(float64)

			apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go fix.r.Start(ctx)

			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if j, ok := fix.r.Last("analyze_document"); ok && j.State == "queued" && j.NotBefore.After(time.Now()) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}

			body, _ := apiGet(t, base, "/api/documents")
			docs, _ := body["documents"].([]any)
			if len(docs) == 0 {
				t.Fatal("no documents")
			}
			doc := docs[0].(map[string]any)
			if doc["state"] != "analysing" {
				t.Errorf("state after %s = %v, want analysing", tc.name, doc["state"])
			}
		})
	}
}

func TestAnalyzeResumesFromCheckpoint(t *testing.T) {
	var sb strings.Builder
	for sb.Len() < 20000 {
		sb.WriteString("Experienced software engineer with strong distributed systems background. ")
	}
	text := sb.String()

	chunks := candidate.SplitChunks(text)
	if len(chunks) < 3 {
		t.Fatalf("need at least 3 chunks, got %d", len(chunks))
	}

	cases := []struct {
		name       string
		addChunk2  func(*models.FakeModel)
		retryAfter bool
		extraCalls int
	}{
		{
			name: "WaitError at chunk two",
			addChunk2: func(fm *models.FakeModel) {
				fm.AddError(models.WaitError{RetryAfter: time.Now().Add(-time.Second)})
			},
			extraCalls: 1,
		},
		{
			name: "invalid JSON at chunk two",
			addChunk2: func(fm *models.FakeModel) {
				fm.AddReply(models.Response{Text: "not valid json"})
				fm.AddReply(models.Response{Text: "still not valid json"})
			},
			retryAfter: true,
			extraCalls: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)

			fm := &models.FakeModel{}
			for i := range chunks {
				if i == 2 {
					tc.addChunk2(fm)
				}
				if i == 1 {
					fm.AddReply(models.Response{Text: fakeProposalReply([]map[string]any{
						{"field": "skill", "value": "Kubernetes", "quote": "distributed systems"},
					})})
				} else {
					fm.AddReply(models.Response{Text: emptyProposalReply()})
				}
			}

			r := runner.New(d, nil, time.Now, slog.Default())
			registerKindsWith(t, r, d, fm)
			base := newRouteServer(t, d, r)

			out, _ := pasteAnalyzeDoc(t, base, "cv.txt", text)
			docID := out["id"].(float64)

			apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go r.Start(ctx)

			if tc.retryAfter {
				waitForDocState(t, base, docID, "error", 5*time.Second)
				waitFor(t, 5*time.Second, func() bool {
					j, ok := r.Last("analyze_document")
					return ok && j.State == "failed"
				})
				apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)
			}
			waitForDocState(t, base, docID, "analysed", 10*time.Second)

			calls := fm.Calls()
			if len(calls) != len(chunks)+tc.extraCalls {
				t.Errorf("model calls = %d, want %d (one per chunk plus %d for the failed attempt at chunk 2)",
					len(calls), len(chunks)+tc.extraCalls, tc.extraCalls)
			}

			listOut, _ := apiGet(t, base, "/api/proposals")
			proposals, _ := listOut["proposals"].([]any)
			found := false
			for _, p := range proposals {
				pm, _ := p.(map[string]any)
				if pm["value"] == "Kubernetes" {
					found = true
				}
			}
			if !found {
				t.Errorf("proposal for Kubernetes (from chunk 1) not in proposals: %v", proposals)
			}
		})
	}
}

func TestAnalyzeCheckpointSurvivesCancel(t *testing.T) {
	d := openTestDB(t)

	var sb strings.Builder
	for sb.Len() < 40000 {
		sb.WriteString("Experienced software engineer with strong distributed systems background. ")
	}
	text := sb.String()

	chunks := candidate.SplitChunks(text)
	if len(chunks) < 4 {
		t.Fatalf("need at least 4 chunks, got %d", len(chunks))
	}

	fm := &models.FakeModel{}
	for range chunks {
		fm.AddReply(models.Response{Text: emptyProposalReply()})
	}
	for range chunks {
		fm.AddReply(models.Response{Text: emptyProposalReply()})
	}
	m := &pauseOnCall4{FakeModel: fm, blocked: make(chan struct{})}

	r1 := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r1, d, m)
	base1 := newRouteServer(t, d, r1)

	out, _ := pasteAnalyzeDoc(t, base1, "cv.txt", text)
	docID := out["id"].(float64)
	apiSend(t, http.MethodPost, base1, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	go r1.Start(ctx1)

	select {
	case <-m.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not reach chunk 3 before timeout")
	}
	cancel1()

	r2 := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r2, d, m)
	base2 := newRouteServer(t, d, r2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go r2.Start(ctx2)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		d.QueryRow(`SELECT state FROM job WHERE kind='analyze_document'`).Scan(&state)
		if state == "interrupted" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	apiSend(t, http.MethodPost, base2, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	waitForDocState(t, base2, docID, "analysed", 10*time.Second)

	calls := fm.Calls()
	if len(calls) != len(chunks) {
		t.Errorf("model calls = %d, want %d (one per chunk despite cancel after chunk 2)",
			len(calls), len(chunks))
	}
}

func TestAnalyzeInterruptedDoesNotAutoRestart(t *testing.T) {
	d := openTestDB(t)

	d.Exec(`INSERT INTO document (name, kind, state, state_reason, char_count, evidence, text, file_name, created_at)
		VALUES ('cv.txt', 'text', 'analysing', '', 100, 0, 'Expert Go engineer.', '', datetime('now'))`)
	var docID int64
	d.QueryRow(`SELECT last_insert_rowid()`).Scan(&docID)

	now := time.Now().UTC().Format(time.RFC3339)
	d.Exec(`INSERT INTO job (kind, subject, priority, state, attempts, not_before, progress, error, params, created, updated)
		VALUES ('analyze_document', ?, 0, 'running', 1, ?, '{}', '', '', ?, ?)`,
		fmt.Sprintf("%d", docID), now, now, now)

	fm := &models.FakeModel{}
	fm.AddReply(models.Response{Text: emptyProposalReply()})

	r := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r, d, fm)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Start(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		d.QueryRow(`SELECT state FROM job WHERE kind='analyze_document'`).Scan(&state)
		if state == "interrupted" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if calls := fm.Calls(); len(calls) != 0 {
		t.Errorf("model calls = %d, want 0 (interrupted job must not auto-restart)", len(calls))
	}
}

func TestAdoptInterruptedDoesNotTakeOtherKind(t *testing.T) {
	fix := newAnalyzeFix(t)
	base := fix.base

	out, _ := pasteAnalyzeDoc(t, base, "cv.txt", "Expert Go engineer with five years of experience.")
	docID := out["id"].(float64)
	subject := fmt.Sprintf("%.0f", docID)

	now := time.Now().UTC().Format(time.RFC3339)
	fix.d.Exec(`INSERT INTO job (kind, subject, priority, state, attempts, not_before, progress, error, params, created, updated)
		VALUES ('other_kind', ?, 0, 'interrupted', 1, ?, '{}', '', '', ?, ?)`,
		subject, now, now, now)
	var otherID int64
	fix.d.QueryRow(`SELECT last_insert_rowid()`).Scan(&otherID)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	var otherState string
	fix.d.QueryRow(`SELECT state FROM job WHERE id=?`, otherID).Scan(&otherState)
	if otherState != "interrupted" {
		t.Errorf("other-kind job state = %q, want interrupted", otherState)
	}

	var analyzeCount int
	fix.d.QueryRow(`SELECT COUNT(*) FROM job WHERE kind='analyze_document' AND state='queued'`).Scan(&analyzeCount)
	if analyzeCount != 1 {
		t.Errorf("queued analyze_document jobs = %d, want 1", analyzeCount)
	}
}

func TestAdoptInterruptedTakesOnlyNewest(t *testing.T) {
	fix := newAnalyzeFix(t)
	base := fix.base

	out, _ := pasteAnalyzeDoc(t, base, "cv.txt", "Expert Go engineer with five years of experience.")
	docID := out["id"].(float64)
	subject := fmt.Sprintf("%.0f", docID)

	now := time.Now().UTC().Format(time.RFC3339)
	fix.d.Exec(`INSERT INTO job (kind, subject, priority, state, attempts, not_before, progress, error, params, created, updated)
		VALUES ('analyze_document', ?, 0, 'interrupted', 1, ?, '{}', '', 'old', ?, ?)`,
		subject, now, now, now)
	var oldID int64
	fix.d.QueryRow(`SELECT last_insert_rowid()`).Scan(&oldID)

	fix.d.Exec(`INSERT INTO job (kind, subject, priority, state, attempts, not_before, progress, error, params, created, updated)
		VALUES ('analyze_document', ?, 0, 'interrupted', 1, ?, '{}', '', 'new', ?, ?)`,
		subject, now, now, now)
	var newID int64
	fix.d.QueryRow(`SELECT last_insert_rowid()`).Scan(&newID)

	apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)

	var oldState string
	fix.d.QueryRow(`SELECT state FROM job WHERE id=?`, oldID).Scan(&oldState)
	if oldState != "interrupted" {
		t.Errorf("old job state = %q, want interrupted (only newest should be adopted)", oldState)
	}

	var newState string
	fix.d.QueryRow(`SELECT state FROM job WHERE id=?`, newID).Scan(&newState)
	if newState != "queued" {
		t.Errorf("new job state = %q, want queued", newState)
	}

	var count int
	fix.d.QueryRow(`SELECT COUNT(*) FROM job WHERE kind='analyze_document' AND state='queued'`).Scan(&count)
	if count != 1 {
		t.Errorf("queued analyze_document jobs = %d, want 1", count)
	}
}

func TestAnalyzeGatePaidBulkBlocked(t *testing.T) {
	var reqCount atomic.Int64
	fakeProv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(fakeProv.Close)

	dir := t.TempDir()
	cfg := fmt.Sprintf(`providers:
  paidbox: {type: openai, model: gpt-turbo, cost: paid, endpoint: %q}
work: {reading: paidbox, shortlist_check: paidbox, preparation: paidbox}
`, fakeProv.URL)
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatalf("write sifty.yaml: %v", err)
	}

	port, cancel := startServer(t, dir, false)
	defer cancel()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	docOut, status := apiSend(t, http.MethodPost, base, "/api/documents",
		map[string]any{"name": "cv.txt", "text": "Experienced engineer with Go and Python skills."})
	if status != http.StatusOK {
		t.Fatalf("upload doc: status %d body %v", status, docOut)
	}
	docID := docOut["id"].(float64)

	_, status = apiSend(t, http.MethodPost, base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)
	if status != http.StatusOK {
		t.Fatalf("POST analyze: status %d", status)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if reqCount.Load() > 0 {
			t.Errorf("paid provider received %d requests, want 0", reqCount.Load())
			return
		}
		docsOut, _ := apiGet(t, base, "/api/documents")
		docs, _ := docsOut["documents"].([]any)
		for _, d := range docs {
			dm, _ := d.(map[string]any)
			if dm["id"] != docID {
				continue
			}
			if s, _ := dm["state"].(string); s != "" && s != "analysing" {
				t.Errorf("document state = %v after ErrBulkOnPaid, want analysing", s)
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if reqCount.Load() != 0 {
		t.Errorf("paid provider received %d requests, want 0", reqCount.Load())
	}
}
