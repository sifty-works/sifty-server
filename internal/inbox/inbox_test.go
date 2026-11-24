package inbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/inbox"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/server"
)

var testNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func testNowFn() time.Time { return testNow }

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func newCat(d *db.DB) *catalog.Catalog {
	return catalog.New(d, func() time.Time { return time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC) })
}

type serverOpts struct {
	collecting    bool
	sourceNames   func() map[string]string
	rules         func() fit.Rules
	changed       func()
	withCandidate bool
}

func newServer(t *testing.T, d *db.DB, o serverOpts) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := server.New(server.Options{Version: "test", Port: 9999, Logger: logger},
		func(mux *http.ServeMux) {
			inbox.RegisterRoutes(mux, d, testNowFn, logger,
				func() bool { return o.collecting }, o.sourceNames, o.rules, o.changed, nil)
			if o.withCandidate {
				candidate.RegisterRoutes(mux, d, "", candidate.Readers{}, logger, nil, nil)
			}
		})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func newTestServer(t *testing.T, d *db.DB) *httptest.Server {
	t.Helper()
	return newServer(t, d, serverOpts{})
}

func apiGetRaw(t *testing.T, ts *httptest.Server, path string) (map[string]any, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	req.Host = "127.0.0.1:9999"
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	return body, resp.StatusCode
}

func apiGet(t *testing.T, ts *httptest.Server, path string) map[string]any {
	t.Helper()
	body, status := apiGetRaw(t, ts, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, status)
	}
	checkShape(t, http.MethodGet, path, body)
	return body
}

func apiJSON(t *testing.T, ts *httptest.Server, method, path string, body any) (map[string]any, int) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
	req.Host = "127.0.0.1:9999"
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode == http.StatusOK {
		checkShape(t, method, path, out)
	}
	return out, resp.StatusCode
}

func checkShape(t *testing.T, method, path string, body map[string]any) {
	t.Helper()
	base, _, _ := strings.Cut(path, "?")
	for _, ri := range server.Routes() {
		m, pat, _ := strings.Cut(ri.Pattern, " ")
		if m != method || ri.RespType == nil || !routeMatch(pat, base) {
			continue
		}
		for _, v := range server.ValidateShape(ri.RespType, body, "") {
			t.Errorf("response shape %s %s: %s", method, path, v)
		}
		return
	}
}

func routeMatch(pat, path string) bool {
	ps := strings.Split(strings.TrimPrefix(pat, "/"), "/")
	qs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(ps) != len(qs) {
		return false
	}
	for i, seg := range ps {
		if !strings.HasPrefix(seg, "{") && seg != qs[i] {
			return false
		}
	}
	return true
}

func getRows(body map[string]any) []any {
	r, _ := body["rows"].([]any)
	return r
}

func field(r any, key string) string {
	m, _ := r.(map[string]any)
	v, _ := m[key].(string)
	return v
}

func rowID(r any) float64 {
	m, _ := r.(map[string]any)
	v, _ := m["id"].(float64)
	return v
}

func rowIDs(body map[string]any) map[float64]bool {
	out := map[float64]bool{}
	for _, r := range getRows(body) {
		out[rowID(r)] = true
	}
	return out
}

func titles(body map[string]any) string {
	var out []string
	for _, r := range getRows(body) {
		out = append(out, field(r, "title"))
	}
	return strings.Join(out, "")
}

func sortedTitles(body map[string]any) string {
	var out []string
	for _, r := range getRows(body) {
		out = append(out, field(r, "title"))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func countOf(body map[string]any, stage string) float64 {
	c, _ := body["counts"].(map[string]any)
	n, _ := c[stage].(float64)
	return n
}

func namedCounts(body map[string]any, key string) map[string]int {
	out := map[string]int{}
	list, _ := body[key].([]any)
	for _, v := range list {
		m, _ := v.(map[string]any)
		name, _ := m["name"].(string)
		n, _ := m["count"].(float64)
		out[name] = int(n)
	}
	return out
}

func addWithSrc(t *testing.T, cat *catalog.Catalog, p listing.Posting, src string) int64 {
	t.Helper()
	id, _, err := cat.Upsert(context.Background(), p, src)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	return id
}

func add(t *testing.T, cat *catalog.Catalog, p listing.Posting) int64 {
	t.Helper()
	return addWithSrc(t, cat, p, "src")
}

func postWithText(url, title, company, text string) listing.Posting {
	p := listing.Posting{URL: url, Title: title, Company: company, Text: text, TextState: listing.TextFull}
	if text == "" {
		p.TextState = listing.TextNotFetched
	}
	return p
}

func post(url, title, company string) listing.Posting {
	return postWithText(url, title, company, "job description")
}

type profileData struct {
	Name     string   `json:"name"`
	Roles    []string `json:"targetRoles,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	AllOf    []string `json:"allOf,omitempty"`
}

func writeProfile(t *testing.T, d *db.DB, id int, pd profileData) {
	t.Helper()
	b, _ := json.Marshal(pd)
	_, err := d.Exec(`INSERT INTO profile (id,data) VALUES (?,?)
		ON CONFLICT(id) DO UPDATE SET data=excluded.data`, id, string(b))
	if err != nil {
		t.Fatalf("writeProfile: %v", err)
	}
}

func setProfileData(t *testing.T, d *db.DB, roles, keywords, allOf []string) {
	t.Helper()
	writeProfile(t, d, 1, profileData{Name: "Profile", Roles: roles, Keywords: keywords, AllOf: allOf})
}

func decide(t *testing.T, d *db.DB, id int64, stage, reason string, at ...string) {
	t.Helper()
	when := testNow.UTC().Format(time.RFC3339)
	if len(at) > 0 {
		when = at[0]
	}
	_, err := d.Exec(`INSERT OR REPLACE INTO decision (profile,vacancy,stage,reason,decided_at)
		VALUES (1,?,?,?,?)`, id, stage, reason, when)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
}

func TestRows_AllFieldsPresent(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	p := post("https://example.com/jobs/1", "Backend Engineer", "Acme Corp")
	p.Location = "Remote · EU"
	p.Published, p.PublishedBasis = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), "datePosted"
	add(t, newCat(d), p)
	rs := getRows(apiGet(t, newTestServer(t, d), "/api/vacancies"))
	if len(rs) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rs))
	}
	for key, want := range map[string]string{
		"company": "Acme Corp", "title": "Backend Engineer", "location": "Remote · EU",
	} {
		if field(rs[0], key) != want {
			t.Errorf("%s: got %q, want %q", key, field(rs[0], key), want)
		}
	}
	if field(rs[0], "published") == "" {
		t.Error("published should not be empty")
	}
}

func TestPublicationLine_Forms(t *testing.T) {
	cases := []struct {
		name    string
		pubAt   time.Time
		basis   string
		wantPub string
	}{
		{"with basis 15 days ago", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), "datePosted", "Published 2 Nov 2026 · 2 weeks"},
		{"without basis", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), "", "Source date 15 Oct 2026 (unverified)"},
		{"today", time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), "datePosted", "Published 17 Nov 2026 · today"},
		{"1 day", time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), "datePosted", "Published 16 Nov 2026 · 1 day"},
		{"5 days", time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), "datePosted", "Published 12 Nov 2026 · 5 days"},
		{"3 weeks", time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), "datePosted", "Published 27 Oct 2026 · 3 weeks"},
		{"3 months (90 days)", time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC), "datePosted", "Published 19 Aug 2026 · 3 months"},
		{"no date", time.Time{}, "", "Publication date not reported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			setProfileData(t, d, nil, []string{"description"}, nil)
			p := post("https://example.com/jobs/pl-"+tc.name, "Engineer", "Acme")
			p.Published = tc.pubAt
			p.PublishedBasis = tc.basis
			add(t, newCat(d), p)
			rs := getRows(apiGet(t, newTestServer(t, d), "/api/vacancies"))
			if len(rs) != 1 {
				t.Fatalf("rows: %d", len(rs))
			}
			if got := field(rs[0], "published"); got != tc.wantPub {
				t.Errorf("published: got %q, want %q", got, tc.wantPub)
			}
		})
	}
}

func TestOrder_NewestFollowsPublicationDateWhetherOrNotItsBasisWasRecorded(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"job"}, nil)
	cat := newCat(d)
	for i, tc := range []struct {
		title string
		day   int
		basis string
	}{
		{"Nov10 no basis", 10, ""},
		{"Nov5 basis", 5, "datePosted"},
		{"Undated", 0, ""},
		{"Nov7 no basis", 7, ""},
		{"Nov1 basis", 1, "datePosted"},
	} {
		p := postWithText(fmt.Sprintf("https://example.com/jobs/ord%d", i), tc.title, "Co", "job")
		if tc.day != 0 {
			p.Published = time.Date(2026, 11, tc.day, 0, 0, 0, 0, time.UTC)
		}
		p.PublishedBasis = tc.basis
		add(t, cat, p)
	}
	rs := getRows(apiGet(t, newTestServer(t, d), "/api/vacancies?sort=newest"))
	if len(rs) != 5 {
		t.Fatalf("rows: %d", len(rs))
	}
	for i, want := range []string{"Nov10 no basis", "Nov7 no basis", "Nov5 basis", "Nov1 basis", "Undated"} {
		if got := field(rs[i], "title"); got != want {
			t.Errorf("row[%d]: got %q, want %q", i, got, want)
		}
	}
}

func TestOrder_TieBreakByFirstSeenThenID(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	t1 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	add(t, catalog.New(d, func() time.Time { return t1 }),
		post("https://example.com/jobs/tie1", "Tie Early", "Co"))
	id2 := add(t, catalog.New(d, func() time.Time { return t2 }),
		post("https://example.com/jobs/tie2", "Tie Later", "Co"))
	rs := getRows(apiGet(t, newTestServer(t, d), "/api/vacancies?sort=newest"))
	if len(rs) < 2 {
		t.Fatalf("rows: %d", len(rs))
	}
	if rowID(rs[0]) != float64(id2) {
		t.Errorf("first row: id %v, want %v (later first_seen)", rowID(rs[0]), id2)
	}
}

func TestSearch(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"backend", "designer", "engineer"}, nil)
	cat := newCat(d)
	p1 := postWithText("https://example.com/jobs/s1", "Golang Backend", "Acme Corp", "orchestration experience")
	p1.Tags = []string{"Kubernetes"}
	add(t, cat, p1)
	p2 := postWithText("https://example.com/jobs/s2", "Frontend Designer", "DeepMind AI", "knowledge of color theory")
	p2.Tags = []string{"Figma"}
	add(t, cat, p2)
	add(t, cat, postWithText("https://example.com/jobs/s3", "TypeScript Engineer", "Acme",
		"Must have OBSERVABILITY and distributed tracing systems"))
	add(t, cat, postWithText("https://example.com/jobs/s4", "Platform Engineer", "Acme", ""))
	ts := newTestServer(t, d)

	for _, tc := range []struct{ q, want string }{
		{"golang", "Golang Backend"}, {"deepmind", "Frontend Designer"}, {"kubernetes", "Golang Backend"},
		{"distributed+tracing", "TypeScript Engineer"}, {"typescript", "TypeScript Engineer"},
		{"TYPESCRIPT", "TypeScript Engineer"}, {"observability", "TypeScript Engineer"},
		{"OBSERVABILITY", "TypeScript Engineer"}, {"platform", "Platform Engineer"},
	} {
		if got := sortedTitles(apiGet(t, ts, "/api/vacancies?q="+tc.q)); got != tc.want {
			t.Errorf("q=%q: titles %q, want %q", tc.q, got, tc.want)
		}
	}

	if countOf(apiGet(t, ts, "/api/vacancies?q=golang"), "inbox") != 4 {
		t.Errorf("counts.inbox with query: want 4")
	}
	if rs := getRows(apiGet(t, ts, "/api/vacancies?q=di")); len(rs) != 0 {
		t.Errorf("2-char query: rows %d, want 0", len(rs))
	}
	for _, q := range []string{"", "+++"} {
		if rs := getRows(apiGet(t, ts, "/api/vacancies?q="+q)); len(rs) != 4 {
			t.Errorf("q=%q: rows %d, want 4", q, len(rs))
		}
	}
}

func TestExclusions_CRUD(t *testing.T) {
	d := openTestDB(t)
	ts := newTestServer(t, d)

	body := apiGet(t, ts, "/api/exclusions")
	if excls, _ := body["exclusions"].([]any); len(excls) != 0 {
		t.Fatalf("initial list: want 0, got %d", len(excls))
	}

	body, status := apiJSON(t, ts, http.MethodPost, "/api/exclusions", map[string]any{"kind": "word", "value": "kotlin"})
	if status != http.StatusOK {
		t.Fatalf("POST: status %d", status)
	}
	excls, _ := body["exclusions"].([]any)
	if len(excls) != 1 {
		t.Fatalf("after add: want 1, got %d", len(excls))
	}
	row, _ := excls[0].(map[string]any)
	id := row["id"].(float64)

	body, status = apiJSON(t, ts, http.MethodDelete, "/api/exclusions", map[string]any{"id": id})
	if status != http.StatusOK {
		t.Fatalf("DELETE: status %d", status)
	}
	if excls, _ := body["exclusions"].([]any); len(excls) != 0 {
		t.Fatalf("after delete: want 0, got %d", len(excls))
	}
}

func TestExclusions_Validation(t *testing.T) {
	d := openTestDB(t)
	ts := newTestServer(t, d)
	apiJSON(t, ts, http.MethodPost, "/api/exclusions", map[string]any{"kind": "word", "value": "ruby"})
	body, status := apiJSON(t, ts, http.MethodPost, "/api/exclusions", map[string]any{"kind": "word", "value": "ruby"})
	if status != http.StatusOK {
		t.Fatalf("duplicate: status %d", status)
	}
	if excls, _ := body["exclusions"].([]any); len(excls) != 1 {
		t.Fatalf("duplicate: want 1, got %d", len(excls))
	}
}

func TestExclusions_Preview(t *testing.T) {
	d := openTestDB(t)
	cat := newCat(d)
	add(t, cat, post("https://example.com/jobs/pv1", "Rust Engineer", "Zephyr"))
	add(t, cat, post("https://example.com/jobs/pv2", "Go Developer", "Globex"))
	ts := newTestServer(t, d)

	for _, tc := range []struct{ kind, value, want string }{
		{"word", "rust", "Would hide 1 role"},
		{"word", "engineer", "Would hide 1 role"},
		{"word", "xyznothing", "Would hide 0 roles"},
		{"company", "Zephyr", "Would hide 1 role"},
		{"company", "Globex", "Would hide 1 role"},
	} {
		body := apiGet(t, ts, "/api/exclusions/preview?kind="+tc.kind+"&value="+tc.value)
		if text, _ := body["text"].(string); text != tc.want {
			t.Errorf("preview kind=%s value=%s: got %q, want %q", tc.kind, tc.value, text, tc.want)
		}
	}
}

func TestExclusions_Filter(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"job"}, nil)
	cat := newCat(d)
	add(t, cat, postWithText("https://example.com/jobs/ex1", "Frontend Developer", "Globex", "job"))
	add(t, cat, postWithText("https://example.com/jobs/ex2", "Backend Engineer", "Acme Corp", "job"))
	p := postWithText("https://example.com/jobs/ex3", "Staff Engineer", "Widget Co", "job")
	p.ApplyURL = "https://boards.greenhouse.io/widgetboard/jobs/999"
	add(t, cat, p)
	ts := newTestServer(t, d)
	rows := func() int { return len(getRows(apiGet(t, ts, "/api/vacancies"))) }
	exclude := func(kind, value string) float64 {
		body, _ := apiJSON(t, ts, http.MethodPost, "/api/exclusions", map[string]any{"kind": kind, "value": value})
		return body["exclusions"].([]any)[0].(map[string]any)["id"].(float64)
	}
	if rows() != 3 {
		t.Fatalf("initial: rows %d, want 3", rows())
	}
	id1 := exclude("word", "frontend")
	if rows() != 2 {
		t.Errorf("word filter: rows %d, want 2", rows())
	}
	if rs := getRows(apiGet(t, ts, "/api/vacancies?q=frontend")); len(rs) != 0 {
		t.Errorf("word search after filter: rows %d, want 0", len(rs))
	}
	apiJSON(t, ts, http.MethodDelete, "/api/exclusions", map[string]any{"id": id1})
	if rows() != 3 {
		t.Errorf("after word remove: rows %d", rows())
	}
	id2 := exclude("company", "Acme Corp")
	if rows() != 2 {
		t.Errorf("company filter: rows %d", rows())
	}
	apiJSON(t, ts, http.MethodDelete, "/api/exclusions", map[string]any{"id": id2})
	exclude("company", "widgetboard")
	if rows() != 2 {
		t.Errorf("greenhouse rule: rows %d", rows())
	}
}

func TestEmpty(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	ts := newTestServer(t, d)
	body := apiGet(t, ts, "/api/vacancies")
	if empty, _ := body["empty"].(string); empty != "Inbox zero. Collect more jobs in Settings → Sources." {
		t.Errorf("empty text: got %q", empty)
	}
	if countOf(body, "inbox") != 0 {
		t.Errorf("counts.inbox: want 0")
	}
	add(t, newCat(d), post("https://example.com/jobs/em1", "Engineer", "Acme"))
	body = apiGet(t, ts, "/api/vacancies?q=zzznomatch")
	if empty, _ := body["empty"].(string); empty != "No matching roles. Adjust your filters." {
		t.Errorf("empty text: got %q", empty)
	}
	if countOf(body, "inbox") != 1 {
		t.Errorf("counts.inbox with query: want 1")
	}
	for stage, want := range map[string]string{
		"shortlist": "Nothing shortlisted yet — press Y in Inbox.",
		"applied":   "No applications yet.",
		"dismissed": "No dismissed roles.",
	} {
		body = apiGet(t, ts, "/api/vacancies?stage="+stage)
		if empty, _ := body["empty"].(string); empty != want {
			t.Errorf("empty %s: got %q, want %q", stage, empty, want)
		}
	}
}

func TestListStages_CountsAndRows(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	cat := newCat(d)
	id1 := add(t, cat, post("https://example.com/jobs/st1", "Alpha", "Co"))
	id2 := add(t, cat, post("https://example.com/jobs/st2", "Beta", "Co"))
	id3 := add(t, cat, post("https://example.com/jobs/st3", "Gamma", "Co"))
	id4 := add(t, cat, post("https://example.com/jobs/st4", "Delta", "Co"))
	ts := newTestServer(t, d)
	decide(t, d, id2, "shortlist", "")
	decide(t, d, id3, "applied", "")
	decide(t, d, id4, "dismissed", "got rejected")

	body := apiGet(t, ts, "/api/vacancies?stage=inbox")
	if rs := getRows(body); len(rs) != 1 || rowID(rs[0]) != float64(id1) {
		t.Errorf("inbox: rows %d, id %v", len(rs), rowID(rs[0]))
	}
	if countOf(body, "inbox") != 1 || countOf(body, "shortlist") != 1 ||
		countOf(body, "applied") != 1 || countOf(body, "dismissed") != 1 || countOf(body, "archive") != 2 {
		t.Errorf("counts mismatch: %v", body["counts"])
	}
	if rs := getRows(apiGet(t, ts, "/api/vacancies?stage=shortlist")); len(rs) != 1 || rowID(rs[0]) != float64(id2) {
		t.Errorf("shortlist: rows %d", len(rs))
	}
	rs := getRows(apiGet(t, ts, "/api/vacancies?stage=applied"))
	if len(rs) != 1 || field(rs[0], "stage") != "applied" {
		t.Errorf("applied: rows %d, stage %q", len(rs), field(rs[0], "stage"))
	}
	rs = getRows(apiGet(t, ts, "/api/vacancies?stage=dismissed"))
	if len(rs) != 1 || field(rs[0], "reason") != "got rejected" {
		t.Errorf("dismissed: rows %d, reason %q", len(rs), field(rs[0], "reason"))
	}
}

func TestListStages_AppliedOrder_AndAppliedText(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	cat := newCat(d)
	id1 := add(t, cat, post("https://example.com/jobs/ao1", "Older", "Co"))
	id2 := add(t, cat, post("https://example.com/jobs/ao2", "Newer", "Co"))
	ts := newTestServer(t, d)

	decide(t, d, id1, "applied", "", "2026-11-01T12:00:00Z")
	decide(t, d, id2, "applied", "", "2026-11-10T12:00:00Z")
	d.Exec(`INSERT INTO application (vacancy,applied_at,profile,profile_name) VALUES (?,?,?,?)`,
		id1, "2026-11-01T12:00:00Z", 1, "Alpha Profile")

	rs := getRows(apiGet(t, ts, "/api/vacancies?stage=applied"))
	if len(rs) != 2 || field(rs[0], "title") != "Newer" {
		t.Errorf("applied order: first title %q, want Newer", field(rs[0], "title"))
	}
	if applied := field(rs[1], "applied"); applied != "Applied 1 Nov 2026" {
		t.Errorf("applied text in applied list: got %q, want 'Applied 1 Nov 2026'", applied)
	}

	id3 := add(t, cat, post("https://example.com/jobs/ao3", "Third", "Co"))
	d.Exec(`INSERT INTO application (vacancy,applied_at,profile,profile_name) VALUES (?,?,?,?)`,
		id3, "2026-10-01T12:00:00Z", 1, "Beta Profile")
	var found string
	for _, r := range getRows(apiGet(t, ts, "/api/vacancies?stage=inbox")) {
		if rowID(r) == float64(id3) {
			found = field(r, "applied")
		}
	}
	if found != "Applied 1 Oct 2026 from Beta Profile" {
		t.Errorf("applied text in inbox: got %q, want 'Applied 1 Oct 2026 from Beta Profile'", found)
	}
}

func TestListStages_ExclusionsApplyToAllStages(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	cat := newCat(d)
	id1 := add(t, cat, post("https://example.com/jobs/ea1", "Kotlin Dev", "Co"))
	add(t, cat, post("https://example.com/jobs/ea2", "Go Dev", "Co"))
	ts := newTestServer(t, d)
	decide(t, d, id1, "shortlist", "")
	apiJSON(t, ts, http.MethodPost, "/api/exclusions", map[string]any{"kind": "word", "value": "kotlin"})

	if rs := getRows(apiGet(t, ts, "/api/vacancies?stage=shortlist")); len(rs) != 0 {
		t.Errorf("shortlist after exclusion: got %d rows, want 0", len(rs))
	}
	if countOf(apiGet(t, ts, "/api/vacancies?stage=inbox"), "shortlist") != 0 {
		t.Errorf("counts.shortlist after exclusion: want 0")
	}
}

func TestDecision_AllowedMoves(t *testing.T) {
	for i, tc := range []struct {
		from, to, reason string
		confirm          bool
	}{
		{"inbox", "shortlist", "", false},
		{"inbox", "dismissed", "", false},
		{"shortlist", "inbox", "", false},
		{"shortlist", "dismissed", "not a fit", false},
		{"shortlist", "applied", "", true},
		{"applied", "shortlist", "", false},
		{"dismissed", "inbox", "", false},
	} {
		t.Run(tc.from+"→"+tc.to, func(t *testing.T) {
			d := openTestDB(t)
			id := add(t, newCat(d), post(fmt.Sprintf("https://example.com/jobs/mv%d", i), "Role", "Co"))
			ts := newTestServer(t, d)
			if tc.from != "inbox" {
				decide(t, d, id, tc.from, "")
			}
			resp, status := apiJSON(t, ts, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/decision", id),
				map[string]any{"profile": 1, "from": tc.from, "to": tc.to, "reason": tc.reason, "confirmed": tc.confirm})
			if status != http.StatusOK || resp["row"] == nil || resp["counts"] == nil {
				t.Errorf("move %s→%s: status %d, row %v, counts %v", tc.from, tc.to, status, resp["row"], resp["counts"])
			}
		})
	}
}

func TestDecision_Refusals(t *testing.T) {
	d := openTestDB(t)
	id := add(t, newCat(d), post("https://example.com/jobs/ref1", "Role", "Co"))
	ts := newTestServer(t, d)
	path := fmt.Sprintf("/api/vacancies/%d/decision", id)
	check := func(label string, wantStatus int, body map[string]any) {
		t.Helper()
		_, status := apiJSON(t, ts, http.MethodPost, path, body)
		if status != wantStatus {
			t.Errorf("%s: status %d, want %d", label, status, wantStatus)
		}
	}
	check("stale from", http.StatusConflict, map[string]any{"profile": 1, "from": "shortlist", "to": "inbox"})
	check("invalid pair", http.StatusUnprocessableEntity, map[string]any{"profile": 1, "from": "inbox", "to": "applied"})
	check("reason on inbox→dismissed", http.StatusUnprocessableEntity, map[string]any{"profile": 1, "from": "inbox", "to": "dismissed", "reason": "not a fit"})

	decide(t, d, id, "shortlist", "")
	check("apply without confirm", http.StatusUnprocessableEntity, map[string]any{"profile": 1, "from": "shortlist", "to": "applied", "confirmed": false})
	check("unknown profile", http.StatusNotFound, map[string]any{"profile": 999, "from": "inbox", "to": "shortlist"})
}

func TestDecision_AppliedWritesApplication_UndoKeepsIt(t *testing.T) {
	d := openTestDB(t)
	id := add(t, newCat(d), post("https://example.com/jobs/app1", "Role", "Co"))
	ts := newTestServer(t, d)
	decide(t, d, id, "shortlist", "")
	path := fmt.Sprintf("/api/vacancies/%d/decision", id)
	appCount := func() int {
		var n int
		d.QueryRow(`SELECT count(*) FROM application WHERE vacancy=?`, id).Scan(&n)
		return n
	}
	_, status := apiJSON(t, ts, http.MethodPost, path, map[string]any{"profile": 1, "from": "shortlist", "to": "applied", "confirmed": true})
	if status != http.StatusOK || appCount() != 1 {
		t.Fatalf("apply: status %d, appCount %d", status, appCount())
	}
	_, status = apiJSON(t, ts, http.MethodPost, path, map[string]any{"profile": 1, "from": "applied", "to": "shortlist"})
	if status != http.StatusOK || appCount() != 1 {
		t.Errorf("undo: status %d, appCount %d (want 1 — application stays)", status, appCount())
	}
}

func TestDecision_AliasedVacancy(t *testing.T) {
	d := openTestDB(t)
	keptID := add(t, newCat(d), post("https://example.com/jobs/al", "Role", "Co"))
	d.Exec(`INSERT INTO vacancy_alias (old_id, new_id) VALUES (99999, ?)`, keptID)
	ts := newTestServer(t, d)
	_, status := apiJSON(t, ts, http.MethodPost, "/api/vacancies/99999/decision",
		map[string]any{"profile": 1, "from": "inbox", "to": "shortlist"})
	if status != http.StatusOK {
		t.Errorf("decision on alias: status %d", status)
	}
	var stage string
	d.QueryRow(`SELECT stage FROM decision WHERE vacancy=? AND profile=1`, keptID).Scan(&stage)
	if stage != "shortlist" {
		t.Errorf("decision written to kept: stage %q", stage)
	}
}

func TestSeen_MarksRead(t *testing.T) {
	d := openTestDB(t)
	id := add(t, newCat(d), post("https://example.com/jobs/seen1", "Role", "Co"))
	ts := newTestServer(t, d)
	_, status := apiJSON(t, ts, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/seen", id), struct{}{})
	if status != http.StatusNoContent {
		t.Fatalf("seen: status %d, want 204", status)
	}
	var seenAt string
	d.QueryRow(`SELECT seen_at FROM vacancy WHERE id=?`, id).Scan(&seenAt)
	if seenAt == "" {
		t.Error("seen_at: should be set after /seen")
	}
	apiJSON(t, ts, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/seen", id), struct{}{})
	var seenAt2 string
	d.QueryRow(`SELECT seen_at FROM vacancy WHERE id=?`, id).Scan(&seenAt2)
	if seenAt2 != seenAt {
		t.Errorf("seen_at changed on repeat call: %q → %q", seenAt, seenAt2)
	}
}

func TestDelete_RemovesVacancy(t *testing.T) {
	d := openTestDB(t)
	cat := newCat(d)
	p := postWithText("https://example.com/jobs/del1", "Role", "Co", "job")
	p.ApplyURL = "https://jobs.lever.co/co/550e8400-e29b-41d4-a716-446655440000"
	id := add(t, cat, p)
	ts := newTestServer(t, d)
	decide(t, d, id, "shortlist", "")
	_, status := apiJSON(t, ts, http.MethodDelete, fmt.Sprintf("/api/vacancies/%d?profile=1", id), nil)
	if status != http.StatusOK {
		t.Fatalf("delete: status %d, want 200", status)
	}
	var count, tkCount int
	d.QueryRow(`SELECT count(*) FROM vacancy WHERE id=?`, id).Scan(&count)
	d.QueryRow(`SELECT count(*) FROM tombstone WHERE key LIKE 'url:%'`).Scan(&tkCount)
	if count != 0 {
		t.Errorf("vacancy still exists after delete")
	}
	if tkCount < 2 {
		t.Errorf("tombstone count: got %d, want >= 2", tkCount)
	}
	_, out, err := cat.Upsert(context.Background(),
		postWithText("https://example.com/jobs/del1", "Role", "Co", ""), "src")
	if err != nil || out != catalog.OutcomeTombstoned {
		t.Errorf("re-upsert: outcome %v err %v, want Tombstoned", out, err)
	}
}

func TestDelete_Refused(t *testing.T) {
	d := openTestDB(t)
	id := add(t, newCat(d), post("https://example.com/jobs/drf1", "Role", "Co"))
	ts := newTestServer(t, d)

	_, status := apiJSON(t, ts, http.MethodDelete, fmt.Sprintf("/api/vacancies/%d?profile=1", id), nil)
	if status != http.StatusConflict {
		t.Errorf("delete in inbox: status %d, want 409", status)
	}

	decide(t, d, id, "shortlist", "")
	collecting := newServer(t, d, serverOpts{collecting: true})
	_, status = apiJSON(t, collecting, http.MethodDelete, fmt.Sprintf("/api/vacancies/%d?profile=1", id), nil)
	if status != http.StatusLocked {
		t.Errorf("delete while collecting: status %d, want 423", status)
	}
}

func TestVacancyView_StageActionsAndStatus(t *testing.T) {
	d := openTestDB(t)
	cat := newCat(d)
	inboxID := add(t, cat, post("https://example.com/jobs/vv1", "R", "Acme"))
	noCoID := add(t, cat, post("https://example.com/jobs/vv2", "R", ""))
	shortID := add(t, cat, post("https://example.com/jobs/vv3", "R", "Co"))
	decide(t, d, shortID, "shortlist", "")
	applID := add(t, cat, post("https://example.com/jobs/vv4", "R", "Co"))
	decide(t, d, applID, "applied", "")
	d.Exec(`INSERT INTO application (vacancy,applied_at,profile,profile_name) VALUES (?,?,1,'P')`, applID, "2026-11-01T12:00:00Z")
	dismID := add(t, cat, post("https://example.com/jobs/vv5", "R", "Co"))
	decide(t, d, dismID, "dismissed", "not a fit")
	ts := newTestServer(t, d)

	for _, tc := range []struct {
		id      int64
		actions string
		status  string
	}{
		{inboxID, "block,dismiss,select", ""},
		{noCoID, "dismiss,select", ""},
		{shortID, "inbox,apply,confirm,delete", ""},
		{applID, "undo,delete", "Applied 1 Nov 2026"},
		{dismID, "restore,delete", "Dismissed — not a fit"},
	} {
		b, _ := apiGetRaw(t, ts, fmt.Sprintf("/api/vacancies/%d?profile=1", tc.id))
		acts, _ := b["actions"].([]any)
		ks := make([]string, len(acts))
		for i, a := range acts {
			m, _ := a.(map[string]any)
			ks[i], _ = m["key"].(string)
		}
		if got := strings.Join(ks, ","); got != tc.actions {
			t.Errorf("actions id=%d: got %q, want %q", tc.id, got, tc.actions)
		}
		if got, _ := b["status"].(string); got != tc.status {
			t.Errorf("status id=%d: got %q, want %q", tc.id, got, tc.status)
		}
	}
}

func TestVacancyView_ExpiredVacancy(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"description"}, nil)
	p := post("https://example.com/jobs/exp1", "R", "Co")
	p.Expires = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	id := add(t, newCat(d), p)
	ts := newTestServer(t, d)
	body, _ := apiGetRaw(t, ts, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
	if exp, _ := body["expired"].(bool); !exp {
		t.Errorf("view: expired should be true")
	}
	for _, r := range getRows(apiGet(t, ts, "/api/vacancies")) {
		if rowID(r) == float64(id) {
			if exp, _ := r.(map[string]any)["expired"].(bool); !exp {
				t.Errorf("list row: expired should be true")
			}
		}
	}
}

func TestVacancyView_ApplyRules(t *testing.T) {
	nowStr := testNow.UTC().Format(time.RFC3339)
	apply := func(url, applyURL string) map[string]any {
		d := openTestDB(t)
		var id int64
		d.QueryRow(`INSERT INTO vacancy (identity,title,company,url,apply_url,first_seen,last_seen,text_state,content_hash,version,body)
			VALUES ('','R','C',?,?,?,?,'full','h',1,'') RETURNING id`, url, applyURL, nowStr, nowStr).Scan(&id)
		d.Exec(`INSERT INTO vacancy_source (vacancy_id,source,url,seen_at) VALUES (?,'s',?,?)`, id, url, nowStr)
		decide(t, d, id, "shortlist", "")
		b, _ := apiGetRaw(t, newTestServer(t, d), fmt.Sprintf("/api/vacancies/%d?profile=1", id))
		for _, a := range b["actions"].([]any) {
			m, _ := a.(map[string]any)
			if k, _ := m["key"].(string); k == "apply" {
				return m
			}
		}
		return nil
	}
	if u, _ := apply("https://ex.com/j1", "https://jobs.lever.co/co/abc")["url"].(string); u != "https://jobs.lever.co/co/abc" {
		t.Errorf("valid apply_url: got %q", u)
	}
	if u, _ := apply("https://ex.com/j2", "https://user:pass@bad.com/j")["url"].(string); u != "https://ex.com/j2" {
		t.Errorf("apply_url with creds: fallback got %q", u)
	}
	if m, _ := apply("ftp://nope/j3", "")["message"].(string); m == "" {
		t.Errorf("both invalid: want message")
	}
}

func TestVacancyView_Sources(t *testing.T) {
	d := openTestDB(t)
	id := add(t, newCat(d), post("https://named.example.com/jobs/f1", "R", "Co"))
	d.Exec(`INSERT OR IGNORE INTO vacancy_source (vacancy_id,source,url,seen_at) VALUES (?,'https://hn.example.org','https://hn.example.org',?)`,
		id, testNow.UTC().Format(time.RFC3339))
	ts := newServer(t, d, serverOpts{
		sourceNames: func() map[string]string { return map[string]string{"src": "Named Board"} },
	})
	body, status := apiGetRaw(t, ts, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
	if status != http.StatusOK {
		t.Fatalf("view: status %d", status)
	}
	srcs, _ := body["sources"].([]any)
	if len(srcs) != 2 {
		t.Fatalf("sources: got %d items, want 2", len(srcs))
	}
	s0, _ := srcs[0].(map[string]any)
	if s0["name"] != "Named Board" {
		t.Errorf("named source name: got %q, want %q", s0["name"], "Named Board")
	}
	if _, hasURL := s0["url"]; hasURL {
		t.Errorf("named source: url should be absent for opaque key, got %q", s0["url"])
	}
	s1, _ := srcs[1].(map[string]any)
	if s1["name"] != "hn.example.org" {
		t.Errorf("unnamed source name: got %q, want %q", s1["name"], "hn.example.org")
	}
	if s1["url"] != "https://hn.example.org" {
		t.Errorf("unnamed source url: got %q, want %q", s1["url"], "https://hn.example.org")
	}
}

func TestVacancyView_SourceNameFromLiveConfig(t *testing.T) {
	d := openTestDB(t)
	id := addWithSrc(t, catalog.New(d, testNowFn),
		post("https://live.example.com/jobs/1", "Staff Engineer", "LiveCo"), "live-board")
	var mu sync.Mutex
	names := map[string]string{}
	ts := newServer(t, d, serverOpts{sourceNames: func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(names)
	}})
	sourceName := func() any {
		b, _ := apiGetRaw(t, ts, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
		srcs, _ := b["sources"].([]any)
		if len(srcs) == 0 {
			t.Fatal("sources empty")
		}
		m, _ := srcs[0].(map[string]any)
		return m["name"]
	}
	if got := sourceName(); got != "live-board" {
		t.Errorf("before config update: name %q, want %q", got, "live-board")
	}
	mu.Lock()
	names["live-board"] = "Live Board"
	mu.Unlock()
	if got := sourceName(); got != "Live Board" {
		t.Errorf("after config update: name %q, want %q", got, "Live Board")
	}
}

func TestVacancyView_ListingState(t *testing.T) {
	for _, tc := range []struct {
		state listing.TextState
		want  string
	}{
		{listing.TextFull, "Full text"},
		{listing.TextSummary, "Listing summary only"},
		{listing.TextBlocked, "Blocked by site"},
		{listing.TextNotFetched, "Not fetched"},
		{listing.TextRemoved, "Text removed after 60 days unseen; it returns if the source shows the role again"},
	} {
		d := openTestDB(t)
		p := postWithText("https://example.com/jobs/ls-"+string(tc.state), "R", "Co", "d")
		p.TextState = tc.state
		id := add(t, newCat(d), p)
		body, _ := apiGetRaw(t, newTestServer(t, d), fmt.Sprintf("/api/vacancies/%d?profile=1", id))
		lv, _ := body["listing"].(map[string]any)
		if s, _ := lv["state"].(string); s != tc.want {
			t.Errorf("state %s: got %q, want %q", tc.state, s, tc.want)
		}
	}
}

func TestVacancyView_AliasedVacancyAndNotFound(t *testing.T) {
	d := openTestDB(t)
	keptID := add(t, newCat(d), post("https://example.com/jobs/vva1", "R", "Co"))
	d.Exec(`INSERT INTO vacancy_alias (old_id, new_id) VALUES (99997, ?)`, keptID)
	ts := newTestServer(t, d)

	body, status := apiGetRaw(t, ts, "/api/vacancies/99997?profile=1")
	if status != http.StatusOK {
		t.Fatalf("alias: status %d", status)
	}
	if id, _ := body["id"].(float64); int64(id) != keptID {
		t.Errorf("alias: id %v, want %d", id, keptID)
	}
	_, status = apiGetRaw(t, ts, "/api/vacancies/99999?profile=1")
	if status != http.StatusNotFound {
		t.Errorf("unknown vacancy: status %d, want 404", status)
	}
	_, status = apiGetRaw(t, ts, fmt.Sprintf("/api/vacancies/%d?profile=99999", keptID))
	if status != http.StatusNotFound {
		t.Errorf("unknown profile: status %d, want 404", status)
	}
}

func TestList_UnreadableProfileIsAnInternalError(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, []string{"engineer"}, nil, nil)
	add(t, newCat(d), post("https://example.com/jobs/np2", "Engineer", "Acme"))
	if _, err := d.Exec(`UPDATE profile SET data='{"targetRoles": 17' WHERE id=1`); err != nil {
		t.Fatalf("corrupt profile: %v", err)
	}
	ts := newTestServer(t, d)

	_, status := apiGetRaw(t, ts, "/api/vacancies?profile=1")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
}

func TestVisibility_ProfileSearch(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		roles, keywords, allOf []string
		exclude, query         string
		posts                  []string
		want                   []string
	}{
		{name: "a target role in the title", roles: []string{"engineer"},
			posts: []string{"Senior Engineer|job description", "Product Manager|job description"},
			want:  []string{"Senior Engineer"}},
		{name: "a target role matches whole words only", roles: []string{"CTO"},
			posts: []string{"Director of Sales|lead the sales team", "Interim CTO (remote)|lead the engineering team"},
			want:  []string{"Interim CTO (remote)"}},
		{name: "a keyword in the title or the text", keywords: []string{"golang"},
			posts: []string{"Golang Backend|systems programming", "Python Developer|data science work"},
			want:  []string{"Golang Backend"}},
		{name: "a role and a keyword must both match", roles: []string{"engineer"}, keywords: []string{"backend"},
			posts: []string{"Backend Engineer|orchestration", "Frontend Engineer|design systems",
				"Backend Specialist|cloud services", "Product Manager|roadmap planning"},
			want: []string{"Backend Engineer"}},
		{name: "neither roles nor keywords show nothing",
			posts: []string{"Engineer|job description", "Developer|job description"}},
		{name: "every all-of term must be present", keywords: []string{"senior"}, allOf: []string{"typescript"},
			posts: []string{"Senior TypeScript Developer|frontend work", "Senior Python Developer|data work",
				"Junior Developer|learning path"},
			want: []string{"Senior TypeScript Developer"}},
		{name: "a short keyword matches a whole word", keywords: []string{"ai"},
			posts: []string{"AI Researcher|language models", "Data Scientist|working with ai systems daily",
				"Engineer|detail-oriented daily work"},
			want: []string{"AI Researcher", "Data Scientist"}},
		{name: "a short keyword matches inside the text", keywords: []string{"JS"},
			posts: []string{"Frontend Developer|Experience with JS, React, and Vue"},
			want:  []string{"Frontend Developer"}},
		{name: "a keyword phrase ignores punctuation", keywords: []string{"Web3.js"},
			posts: []string{"Web3 JS developer|"},
			want:  []string{"Web3 JS developer"}},
		{name: "a word exclusion of this profile hides a role", keywords: []string{"role"}, exclude: "restricted",
			posts: []string{"Restricted Role|some work", "Open Role|other work"},
			want:  []string{"Open Role"}},
		{name: "a query ignores punctuation in the text", keywords: []string{"developer"}, query: "node.js",
			posts: []string{"Backend Developer|Using Node.js for server-side rendering", "Backend Tester|developer of plain work"},
			want:  []string{"Backend Developer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			setProfileData(t, d, tc.roles, tc.keywords, tc.allOf)
			cat := newCat(d)
			for i, p := range tc.posts {
				title, text, _ := strings.Cut(p, "|")
				add(t, cat, postWithText(fmt.Sprintf("https://example.com/jobs/vis%d", i), title, "Acme", text))
			}
			if tc.exclude != "" {
				d.Exec(`INSERT INTO exclusion (kind,value,norm,profile_id,created) VALUES ('word',?,?,1,'2026-01-01T00:00:00Z')`,
					tc.exclude, tc.exclude)
			}
			body := apiGet(t, newTestServer(t, d), "/api/vacancies?q="+tc.query)
			sort.Strings(tc.want)
			if got := sortedTitles(body); got != strings.Join(tc.want, ",") {
				t.Errorf("rows: got %q, want %q", got, strings.Join(tc.want, ","))
			}
			if tc.query == "" && int(countOf(body, "inbox")) != len(tc.want) {
				t.Errorf("counts.inbox: got %v, want %d", countOf(body, "inbox"), len(tc.want))
			}
		})
	}
}

func TestVisibility_DecidedVacancyStaysWhenSearchNoLongerFits(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"golang"}, nil)
	id1 := add(t, newCat(d), postWithText("https://example.com/jobs/dv1", "Golang Developer", "Acme", "systems programming"))
	ts := newTestServer(t, d)
	decide(t, d, id1, "shortlist", "")

	setProfileData(t, d, nil, []string{"python"}, nil)

	if rs := getRows(apiGet(t, ts, "/api/vacancies?stage=shortlist")); len(rs) != 1 {
		t.Errorf("shortlist after search change: rows %d, want 1", len(rs))
	}
	body := apiGet(t, ts, "/api/vacancies?stage=inbox")
	if countOf(body, "inbox") != 0 {
		t.Errorf("inbox count after search change: %v, want 0", countOf(body, "inbox"))
	}
	if countOf(body, "shortlist") != 1 {
		t.Errorf("shortlist count after search change: %v, want 1", countOf(body, "shortlist"))
	}
}
