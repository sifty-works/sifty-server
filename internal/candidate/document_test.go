package candidate_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/server"
)

func newDocServer(t *testing.T, d *db.DB) *httptest.Server {
	return newDocServerWithFiles(t, d, t.TempDir())
}

func newDocServerWithFiles(t *testing.T, d *db.DB, filesDir string) *httptest.Server {
	t.Helper()
	return newDocServerWithPDF(t, d, filesDir, candidate.ProgramText{Program: filepath.Join(t.TempDir(), "absent")})
}

func newDocServerWithPDF(t *testing.T, d *db.DB, filesDir string, pdfText candidate.TextSource) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := server.New(server.Options{
		Version:           "test",
		Port:              9999,
		Logger:            logger,
		BinaryUploadPaths: []string{"/api/me/photo", "/api/documents"},
	}, func(mux *http.ServeMux) {
		candidate.RegisterRoutes(mux, d, filesDir, pdfText, logger, nil, nil)
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func uploadDoc(t *testing.T, ts *httptest.Server, name string, body []byte, contentType string) (map[string]any, int) {
	t.Helper()
	url := ts.URL + "/api/documents"
	if name != "" {
		url += "?name=" + name
	}
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Host = "127.0.0.1:9999"
	req.Header.Set("Content-Type", contentType)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /api/documents: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}

func pasteDoc(t *testing.T, ts *httptest.Server, name, text string) (map[string]any, int) {
	t.Helper()
	return send(t, ts, http.MethodPost, "/api/documents", map[string]any{"name": name, "text": text})
}

func buildDocx(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatalf("build docx: %v", err)
	}
	fmt.Fprintf(f, `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>%s</w:t></w:r></w:p></w:body></w:document>`, text)
	if err := w.Close(); err != nil {
		t.Fatalf("build docx close: %v", err)
	}
	return buf.Bytes()
}

func buildPDF() []byte {
	return []byte("%PDF-1.4 fake pdf content for testing purposes only")
}

func getDoc(t *testing.T, ts *httptest.Server, path string) (map[string]any, int) {
	t.Helper()
	return get(t, ts, path)
}

func TestUploadKindsTable(t *testing.T) {
	docxText := "Engineer"
	docxCharCount := float64(len([]rune(docxText)))

	cases := []struct {
		name          string
		filename      string
		body          []byte
		ct            string
		wantKind      string
		wantState     string
		wantCharCount float64
	}{
		{
			name:          "txt kind state and exact charCount",
			filename:      "notes.txt",
			body:          []byte("hello world"),
			ct:            "text/plain",
			wantKind:      "txt",
			wantState:     "ready",
			wantCharCount: float64(len([]rune("hello world"))),
		},
		{
			name:          "md kind state and exact charCount",
			filename:      "readme.md",
			body:          []byte("# Title"),
			ct:            "text/plain",
			wantKind:      "md",
			wantState:     "ready",
			wantCharCount: float64(len([]rune("# Title"))),
		},
		{
			name:          "docx kind state and exact charCount",
			filename:      "cv.docx",
			body:          nil,
			ct:            "application/zip",
			wantKind:      "docx",
			wantState:     "ready",
			wantCharCount: docxCharCount,
		},
	}

	d := openTestDB(t)
	ts := newDocServer(t, d)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if tc.filename == "cv.docx" {
				body = buildDocx(t, docxText)
			}
			out, status := uploadDoc(t, ts, tc.filename, body, tc.ct)
			if status != http.StatusOK {
				t.Fatalf("upload %s: status %d, body: %v", tc.filename, status, out)
			}
			if out["kind"] != tc.wantKind {
				t.Errorf("kind = %v, want %v", out["kind"], tc.wantKind)
			}
			if out["state"] != tc.wantState {
				t.Errorf("state = %v, want %v", out["state"], tc.wantState)
			}
			if out["charCount"] != tc.wantCharCount {
				t.Errorf("charCount = %v, want %v", out["charCount"], tc.wantCharCount)
			}
		})
	}
}

func TestDocxExtractionMultipleStructures(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"` +
		` xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006">` +
		`<w:body>` +
		`<w:p><w:r><w:t>FirstParagraph</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>SecondParagraph</w:t></w:r></w:p>` +
		`<w:tbl><w:tr>` +
		`<w:tc><w:p><w:r><w:t>CellAlpha</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>CellBeta</w:t></w:r></w:p></w:tc>` +
		`</w:tr></w:tbl>` +
		`<w:p><w:r><w:t>Tab</w:t></w:r><w:r><w:tab/></w:r><w:r><w:t>Separated</w:t></w:r></w:p>` +
		`<mc:AlternateContent>` +
		`<mc:Choice Requires="wps"><w:p><w:r><w:t>TextBoxContent</w:t></w:r></w:p></mc:Choice>` +
		`<mc:Fallback><w:p><w:r><w:t>TextBoxContent</w:t></w:r></w:p></mc:Fallback>` +
		`</mc:AlternateContent>` +
		`</w:body></w:document>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("build docx: %v", err)
	}
	fmt.Fprint(f, docXML)
	if err := zw.Close(); err != nil {
		t.Fatalf("build docx close: %v", err)
	}

	d := openTestDB(t)
	ts := newDocServer(t, d)

	out, status := uploadDoc(t, ts, "complex.docx", buf.Bytes(), "application/zip")
	if status != http.StatusOK {
		t.Fatalf("upload complex docx: status %d, body: %v", status, out)
	}
	id := out["id"]
	idStr := fmt.Sprintf("%.0f", id)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/documents/"+idStr+"/text", nil)
	req.Host = "127.0.0.1:9999"
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /text: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("text endpoint: status %d, want 200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	text := string(got)

	for _, want := range []string{"FirstParagraph", "SecondParagraph", "CellAlpha", "CellBeta", "Tab", "Separated", "TextBoxContent"} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q; got: %q", want, text)
		}
	}
	if strings.Contains(text, "TabSeparated") {
		t.Errorf("tab did not separate words: got %q", text)
	}
	if n := strings.Count(text, "TextBoxContent"); n != 1 {
		t.Errorf("TextBoxContent appears %d times, want exactly 1 (AlternateContent fallback must be skipped); got: %q", n, text)
	}
}

func makeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pdftotext.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return p
}

func docText(t *testing.T, ts *httptest.Server, id any) (string, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/documents/%.0f/text", ts.URL, id), nil)
	req.Host = "127.0.0.1:9999"
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET text: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode
}

func refreshDoc(t *testing.T, ts *httptest.Server, id any) map[string]any {
	t.Helper()
	out, status := send(t, ts, http.MethodPost, fmt.Sprintf("/api/documents/%.0f/refresh", id), map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("refresh: status %d, body: %v", status, out)
	}
	return out
}

func TestPdfIsReadByTheProgramFromItsStandardInput(t *testing.T) {
	work := t.TempDir()
	script := makeScript(t, fmt.Sprintf("printf '%%s\\n' \"$@\" > %[1]s/args\ncat > %[1]s/stdin\nprintf 'Jane Roe\\fSecond page\\n'", work))
	ts := newDocServerWithPDF(t, openTestDB(t), t.TempDir(), candidate.ProgramText{Program: script, Args: []string{"-", "-"}})

	body := buildPDF()
	out, status := uploadDoc(t, ts, "cv.pdf", body, "application/pdf")
	if status != http.StatusOK {
		t.Fatalf("upload: status %d, body: %v", status, out)
	}
	want := "Jane Roe\nSecond page"
	if out["state"] != "ready" || out["charCount"] != float64(len(want)) {
		t.Errorf("state %v, charCount %v; want ready, %d", out["state"], out["charCount"], len(want))
	}
	if text, _ := docText(t, ts, out["id"]); text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if args, _ := os.ReadFile(filepath.Join(work, "args")); string(args) != "-\n-\n" {
		t.Errorf("arguments = %q, want a dash twice", args)
	}
	if in, _ := os.ReadFile(filepath.Join(work, "stdin")); !bytes.Equal(in, body) {
		t.Errorf("the program did not receive the stored bytes on standard input")
	}
}

func TestPdfWithoutTheProgramIsStoredWithAnInstallReason(t *testing.T) {
	ts := newDocServer(t, openTestDB(t))

	out, status := uploadDoc(t, ts, "cv.pdf", buildPDF(), "application/pdf")
	if status != http.StatusOK {
		t.Fatalf("upload: status %d, body: %v", status, out)
	}
	if out["state"] != "unavailable" || out["charCount"] != float64(0) {
		t.Errorf("state %v, charCount %v; want unavailable, 0", out["state"], out["charCount"])
	}
	if reason, _ := out["stateReason"].(string); !strings.Contains(reason, "poppler") {
		t.Errorf("stateReason = %q, want it to name poppler", reason)
	}
	if _, status := docText(t, ts, out["id"]); status != http.StatusNotFound {
		t.Errorf("text status = %d, want 404 for a document with no text", status)
	}
}

func TestRefreshReadsAStoredPdfOnceTheProgramIsInstalled(t *testing.T) {
	work := t.TempDir()
	program := filepath.Join(work, "pdftotext.sh")
	ts := newDocServerWithPDF(t, openTestDB(t), t.TempDir(), candidate.ProgramText{Program: program})

	out, _ := uploadDoc(t, ts, "cv.pdf", buildPDF(), "application/pdf")
	if out["state"] != "unavailable" {
		t.Fatalf("state = %v, want unavailable", out["state"])
	}
	script := fmt.Sprintf("echo run >> %s/calls\nprintf 'Jane Roe'", work)
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	item := refreshDoc(t, ts, out["id"])
	if item["state"] != "ready" || item["stateReason"] != nil || item["charCount"] != float64(8) {
		t.Errorf("after refresh: %v, want ready with 8 characters and no reason", item)
	}
	if text, _ := docText(t, ts, out["id"]); text != "Jane Roe" {
		t.Errorf("text = %q, want Jane Roe", text)
	}
	refreshDoc(t, ts, out["id"])
	if calls, _ := os.ReadFile(filepath.Join(work, "calls")); strings.Count(string(calls), "run") != 1 {
		t.Errorf("program ran %q times, want once: a document with text is not read again", calls)
	}
}

func TestPdfProgramFailureLeavesAReasonAndNoText(t *testing.T) {
	cases := []struct {
		name       string
		script     string
		timeout    time.Duration
		maxOutput  int
		wantReason string
	}{
		{"exits with an error", "echo 'broken xref table' >&2\nexit 1", 0, 0, "could not be read"},
		{"writes more than the cap", "printf '%0200d' 7", 0, 64, "too long to keep"},
		{"never returns", "sleep 5 & wait", 200 * time.Millisecond, 0, "too long and was stopped"},
		{"writes nothing", "exit 0", 0, 0, "No text was found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := candidate.ProgramText{Program: makeScript(t, tc.script), Timeout: tc.timeout, MaxOutput: tc.maxOutput}
			ts := newDocServerWithPDF(t, openTestDB(t), t.TempDir(), src)
			start := time.Now()
			out, status := uploadDoc(t, ts, "cv.pdf", buildPDF(), "application/pdf")
			if status != http.StatusOK {
				t.Fatalf("upload: status %d, body: %v", status, out)
			}
			if time.Since(start) > 4*time.Second {
				t.Errorf("the upload waited %v for the program", time.Since(start))
			}
			reason, _ := out["stateReason"].(string)
			if out["state"] != "error" || out["charCount"] != float64(0) || !strings.Contains(reason, tc.wantReason) {
				t.Errorf("state %v, charCount %v, reason %q; want error, 0, %q", out["state"], out["charCount"], reason, tc.wantReason)
			}
			if strings.Contains(reason, "xref") {
				t.Errorf("reason %q carries the program's error output", reason)
			}
			if _, status := docText(t, ts, out["id"]); status != http.StatusNotFound {
				t.Errorf("text status = %d, want 404", status)
			}
		})
	}
}

func TestBytesNotMatchingNameIsRefused(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	pdfBody := buildPDF()
	out, status := uploadDoc(t, ts, "notes.txt", pdfBody, "application/pdf")
	if status != http.StatusUnprocessableEntity {
		t.Errorf("pdf bytes as txt: status %d, want 422, body: %v", status, out)
	}
	errMsg, _ := out["error"].(string)
	if !strings.Contains(errMsg, "plain text") {
		t.Errorf("error message %q should mention plain text", errMsg)
	}
}

func TestPastedTextDocumentArrives(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	out, status := pasteDoc(t, ts, "My notes", "Some important text here")
	if status != http.StatusOK {
		t.Fatalf("paste text: status %d, body: %v", status, out)
	}
	if out["kind"] != "text" {
		t.Errorf("kind = %v, want text", out["kind"])
	}
	if out["state"] != "ready" {
		t.Errorf("state = %v, want ready", out["state"])
	}
	if out["name"] != "My notes" {
		t.Errorf("name = %v, want My notes", out["name"])
	}
}

func TestListIsNewestFirst(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	uploadDoc(t, ts, "first.txt", []byte("first document"), "text/plain")
	uploadDoc(t, ts, "second.txt", []byte("second document"), "text/plain")
	uploadDoc(t, ts, "third.txt", []byte("third document"), "text/plain")

	body, status := getDoc(t, ts, "/api/documents")
	if status != http.StatusOK {
		t.Fatalf("GET /api/documents: status %d", status)
	}
	docs, _ := body["documents"].([]any)
	if len(docs) != 3 {
		t.Fatalf("documents count = %d, want 3", len(docs))
	}
	first, _ := docs[0].(map[string]any)
	if first["name"] != "third.txt" {
		t.Errorf("first item name = %v, want third.txt (newest first)", first["name"])
	}
	last, _ := docs[2].(map[string]any)
	if last["name"] != "first.txt" {
		t.Errorf("last item name = %v, want first.txt (newest first)", last["name"])
	}
}

func TestDeleteRemovesDocumentAndFile(t *testing.T) {
	d := openTestDB(t)
	filesDir := t.TempDir()
	ts := newDocServerWithFiles(t, d, filesDir)

	out, _ := uploadDoc(t, ts, "cv.txt", []byte("resume content"), "text/plain")
	id := out["id"]
	idStr := fmt.Sprintf("%.0f", id)

	before, err := os.ReadDir(filesDir)
	if err != nil {
		t.Fatalf("ReadDir before delete: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("before delete: filesDir has %d files, want 1", len(before))
	}

	_, status := send(t, ts, http.MethodDelete, "/api/documents/"+idStr, nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE /api/documents/%s: status %d, want 204", idStr, status)
	}

	after, err := os.ReadDir(filesDir)
	if err != nil {
		t.Fatalf("ReadDir after delete: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("after delete: filesDir has %d files, want 0 (file not removed from disk)", len(after))
	}

	listBody, _ := getDoc(t, ts, "/api/documents")
	docs, _ := listBody["documents"].([]any)
	if len(docs) != 0 {
		t.Errorf("after delete: document still in list")
	}
}

func TestFileNamingPreventsPathTraversalAndCollision(t *testing.T) {
	d := openTestDB(t)
	baseDir := t.TempDir()
	filesDir := baseDir + "/files"
	ts := newDocServerWithFiles(t, d, filesDir)

	out1, s1 := uploadDoc(t, ts, "../../escape.txt", []byte("escape content"), "text/plain")
	if s1 != http.StatusOK {
		t.Fatalf("escape upload: status %d, body: %v", s1, out1)
	}
	out2, s2 := uploadDoc(t, ts, "CV.txt", []byte("content one"), "text/plain")
	if s2 != http.StatusOK {
		t.Fatalf("cv1 upload: status %d", s2)
	}
	out3, s3 := uploadDoc(t, ts, "CV.txt", []byte("content two"), "text/plain")
	if s3 != http.StatusOK {
		t.Fatalf("cv2 upload: status %d", s3)
	}

	baseEntries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("ReadDir baseDir: %v", err)
	}
	if len(baseEntries) != 1 || baseEntries[0].Name() != "files" {
		names := make([]string, len(baseEntries))
		for i, e := range baseEntries {
			names[i] = e.Name()
		}
		t.Errorf("baseDir contains %v, want only [files] (path traversal may have written outside)", names)
	}

	fileEntries, err := os.ReadDir(filesDir)
	if err != nil {
		t.Fatalf("ReadDir filesDir: %v", err)
	}
	if len(fileEntries) != 3 {
		t.Fatalf("filesDir has %d files, want 3", len(fileEntries))
	}
	for _, e := range fileEntries {
		if !strings.HasPrefix(e.Name(), "doc_") {
			t.Errorf("file %q should be named doc_N", e.Name())
		}
	}

	for _, tc := range []struct {
		out     map[string]any
		content string
	}{
		{out1, "escape content"},
		{out2, "content one"},
		{out3, "content two"},
	} {
		idStr := fmt.Sprintf("%.0f", tc.out["id"])
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/documents/"+idStr+"/original", nil)
		req.Host = "127.0.0.1:9999"
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET /original: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("/original status %d, want 200", resp.StatusCode)
		}
		if string(body) != tc.content {
			t.Errorf("original content %q, want %q", string(body), tc.content)
		}
	}
}

func TestEvidenceSurvivesReload(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	out, _ := uploadDoc(t, ts, "notes.txt", []byte("some notes"), "text/plain")
	id := out["id"]
	idStr := fmt.Sprintf("%.0f", id)

	patched, status := send(t, ts, http.MethodPatch, "/api/documents/"+idStr, map[string]any{"evidence": true})
	if status != http.StatusOK {
		t.Fatalf("PATCH evidence: status %d, body: %v", status, patched)
	}
	if patched["evidence"] != true {
		t.Errorf("evidence after PATCH = %v, want true", patched["evidence"])
	}

	listBody, _ := getDoc(t, ts, "/api/documents")
	docs, _ := listBody["documents"].([]any)
	if len(docs) != 1 {
		t.Fatalf("reload: docs count = %d", len(docs))
	}
	doc, _ := docs[0].(map[string]any)
	if doc["evidence"] != true {
		t.Errorf("evidence after reload = %v, want true", doc["evidence"])
	}
}

func TestTextEndpointReturnsExtractedText(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	content := "this is the document text"
	out, _ := uploadDoc(t, ts, "doc.txt", []byte(content), "text/plain")
	id := out["id"]
	idStr := fmt.Sprintf("%.0f", id)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/documents/"+idStr+"/text", nil)
	req.Host = "127.0.0.1:9999"
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /api/documents/%s/text: %v", idStr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("text endpoint: status %d, want 200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(got)) != content {
		t.Errorf("text = %q, want %q", string(got), content)
	}
}

func TestTextEndpointUnavailableForPdf(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	out, _ := uploadDoc(t, ts, "cv.pdf", buildPDF(), "application/pdf")
	id := out["id"]
	idStr := fmt.Sprintf("%.0f", id)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/documents/"+idStr+"/text", nil)
	req.Host = "127.0.0.1:9999"
	resp, _ := ts.Client().Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("pdf text endpoint: status %d, want 404", resp.StatusCode)
	}
}

func TestOriginalEndpointNotFoundForPastedText(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	out, _ := pasteDoc(t, ts, "My notes", "Some text")
	id := out["id"]
	idStr := fmt.Sprintf("%.0f", id)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/documents/"+idStr+"/original", nil)
	req.Host = "127.0.0.1:9999"
	resp, _ := ts.Client().Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("pasted text original: status %d, want 404", resp.StatusCode)
	}
}

func TestWrongExtensionIsRefused(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	_, status := uploadDoc(t, ts, "document.exe", []byte("some bytes"), "application/octet-stream")
	if status != http.StatusUnprocessableEntity {
		t.Errorf("wrong extension: status %d, want 422", status)
	}
}

func TestZipWithoutDocumentXmlIsStoredWithErrorState(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("not_a_word_file.txt")
	if err != nil {
		t.Fatalf("build zip: %v", err)
	}
	fmt.Fprint(f, "not a docx")
	if err := w.Close(); err != nil {
		t.Fatalf("build zip close: %v", err)
	}

	out, status := uploadDoc(t, ts, "fake.docx", buf.Bytes(), "application/zip")
	if status != http.StatusOK {
		t.Fatalf("zip without document.xml: status %d, body: %v", status, out)
	}
	if out["state"] != "error" {
		t.Errorf("state = %v, want error for zip without word/document.xml", out["state"])
	}
}

func TestEmptyFileIsRefused(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	out, status := uploadDoc(t, ts, "empty.txt", []byte{}, "text/plain")
	if status != http.StatusUnprocessableEntity {
		t.Errorf("empty file: status %d, want 422, body: %v", status, out)
	}
	errMsg, _ := out["error"].(string)
	if !strings.Contains(strings.ToLower(errMsg), "empty") {
		t.Errorf("error message %q should mention empty", errMsg)
	}
}

func TestDeleteNonExistentDocumentIs404(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	_, status := send(t, ts, http.MethodDelete, "/api/documents/9999", nil)
	if status != http.StatusNotFound {
		t.Errorf("delete nonexistent: status %d, want 404", status)
	}
}

func TestPatchNonExistentDocumentIs404(t *testing.T) {
	d := openTestDB(t)
	ts := newDocServer(t, d)

	_, status := send(t, ts, http.MethodPatch, "/api/documents/9999", map[string]any{"evidence": true})
	if status != http.StatusNotFound {
		t.Errorf("patch nonexistent: status %d, want 404", status)
	}
}
