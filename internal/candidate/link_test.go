package candidate_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
)

const aboutPage = `<html><head><title>About Jane</title><script>var x = 1;</script></head>
<body><h1>Jane Roe</h1><p>Backend engineer working on search.</p><ul><li>Go</li><li>SQLite</li></ul></body></html>`

func pageEntry(url, contentType, body string) fetchtest.MapEntry {
	return fetchtest.MapEntry{Response: fetch.Response{URL: url, Status: 200, ContentType: contentType, Body: []byte(body)}}
}

func newLinkServer(t *testing.T, pages *fetchtest.Map) (*httptest.Server, *db.DB) {
	t.Helper()
	d := openTestDB(t)
	return newDocServerWithReaders(t, d, t.TempDir(), candidate.Readers{Pages: pages}), d
}

func addLink(t *testing.T, ts *httptest.Server, address string) (map[string]any, int) {
	t.Helper()
	return send(t, ts, http.MethodPost, "/api/documents", map[string]any{"url": address})
}

func documentCount(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	list, _ := get(t, ts, "/api/documents")
	docs, _ := list["documents"].([]any)
	return len(docs)
}

func TestLinkErrorsEachSayWhatWentWrongAndLeaveNoDocument(t *testing.T) {
	const address = "https://example.com/page"
	cases := []struct {
		name  string
		typed string
		entry *fetchtest.MapEntry
		want  string
	}{
		{"no address", "  ", nil, "Enter a URL."},
		{"not a web address", "ftp://example.com/page", nil, "Enter a web address that starts with http:// or https://."},
		{"private address", address, &fetchtest.MapEntry{Err: fetch.ErrRefused}, "That address is private or not allowed, so Sifty will not open it."},
		{"login or refusal", address, &fetchtest.MapEntry{Err: fetch.ErrBlocked}, "The page cannot be read automatically. It may need a login, or the site may refuse automatic reading."},
		{"refusal by status", address, &fetchtest.MapEntry{Response: fetch.Response{Status: 403}}, "The page cannot be read automatically. It may need a login, or the site may refuse automatic reading."},
		{"not found", address, &fetchtest.MapEntry{Err: fetch.ErrNotFound}, "The page was not found."},
		{"site not answering", address, &fetchtest.MapEntry{Err: fetch.ErrTransient}, "The page could not be loaded. Try again later."},
		{"offline", address, &fetchtest.MapEntry{Err: fetch.ErrOffline}, "Sifty is offline, so it cannot read pages."},
		{"not HTML", address, ptr(pageEntry(address, "application/json", `{"a": 1}`)), "The address does not lead to a web page."},
		{"no text", address, ptr(pageEntry(address, "text/html", "<html><body><script>x()</script></body></html>")), "The page has no text."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pages := fetchtest.NewMap()
			if tc.entry != nil {
				pages.Set(address, *tc.entry)
			}
			ts, _ := newLinkServer(t, pages)
			out, status := addLink(t, ts, tc.typed)
			if status != http.StatusUnprocessableEntity || out["error"] != tc.want {
				t.Errorf("status %d, body %v; want 422 with %q", status, out, tc.want)
			}
			if n := documentCount(t, ts); n != 0 {
				t.Errorf("%d documents after a refused link, want none", n)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestSameAddressInTwoFormsGivesOneDocument(t *testing.T) {
	pages := fetchtest.NewMap()
	pages.Set("https://www.example.com/jobs/", pageEntry("https://www.example.com/jobs/", "text/html", aboutPage))
	ts, _ := newLinkServer(t, pages)

	first, _ := addLink(t, ts, "https://www.example.com/jobs/")
	second, status := addLink(t, ts, "https://Example.com/jobs?utm_source=mail&ref=x#top")
	if status != http.StatusOK || second["id"] != first["id"] {
		t.Errorf("second add: status %d, id %v; want the first document %v", status, second["id"], first["id"])
	}
	if n := documentCount(t, ts); n != 1 {
		t.Errorf("%d documents, want 1", n)
	}
	if reqs := pages.Requests(); len(reqs) != 1 {
		t.Errorf("requests = %v, want one: the existing document is returned without reading the page again", reqs)
	}
}

func TestAddAndRefreshRequestTheAddressAsTyped(t *testing.T) {
	const typed = "https://www.example.com/jobs/?utm_source=mail"
	pages := fetchtest.NewMap()
	pages.Set(typed, pageEntry(typed, "text/html", aboutPage))
	ts, _ := newLinkServer(t, pages)

	doc, status := addLink(t, ts, typed)
	if status != http.StatusOK || doc["address"] != typed {
		t.Fatalf("add: status %d, document %v; want the address as typed", status, doc)
	}
	if out, status := refreshLink(t, ts, doc["id"]); status != http.StatusOK {
		t.Fatalf("refresh: status %d, body %v", status, out)
	}
	if reqs := pages.Requests(); len(reqs) != 2 || reqs[0] != typed || reqs[1] != typed {
		t.Errorf("requests = %v, want the typed address both times", reqs)
	}
}

type racingFetcher struct {
	*fetchtest.Map
	d *db.DB
}

func (f racingFetcher) Get(ctx context.Context, url string, opt fetch.Opt) (fetch.Response, error) {
	f.d.Exec(`INSERT INTO document (name, kind, state, created_at, address, address_key) VALUES ('rival', 'text', 'ready', 'now', ?, 'https://example.com/jobs')`, url)
	return f.Map.Get(ctx, url, opt)
}

func TestTwoAddsOfOneAddressAtOnceAnswerWithTheStoredDocument(t *testing.T) {
	pages := fetchtest.NewMap()
	pages.Set("https://example.com/jobs", pageEntry("https://example.com/jobs", "text/html", aboutPage))
	d := openTestDB(t)
	ts := newDocServerWithReaders(t, d, t.TempDir(), candidate.Readers{Pages: racingFetcher{pages, d}})

	out, status := addLink(t, ts, "https://example.com/jobs")
	if status != http.StatusOK || out["name"] != "rival" {
		t.Errorf("status %d, document %v; want the document the other add stored", status, out)
	}
	if n := documentCount(t, ts); n != 1 {
		t.Errorf("%d documents, want 1", n)
	}
}

func refreshLink(t *testing.T, ts *httptest.Server, id any) (map[string]any, int) {
	t.Helper()
	return send(t, ts, http.MethodPost, fmt.Sprintf("/api/documents/%.0f/refresh", id), map[string]any{})
}

func TestRefreshOfALinkWritesNothingWhenTheTextIsUnchanged(t *testing.T) {
	pages := fetchtest.NewMap()
	pages.Set("https://example.com/about", pageEntry("https://example.com/about", "text/html", aboutPage))
	ts, d := newLinkServer(t, pages)
	doc, _ := addLink(t, ts, "https://example.com/about")
	if _, err := d.Exec(`UPDATE document SET state='analysed', state_reason='3' WHERE id=?`, doc["id"]); err != nil {
		t.Fatalf("mark analysed: %v", err)
	}

	pages.Set("https://example.com/about", pageEntry("https://example.com/about", "text/html", strings.Replace(aboutPage, "<script>var x = 1;</script>", "<script>var y = 2;</script>", 1)))
	out, status := refreshLink(t, ts, doc["id"])
	if status != http.StatusOK || out["state"] != "analysed" || out["stateReason"] != "3" {
		t.Errorf("status %d, document %v; want it untouched, still analysed with its reason", status, out)
	}
	if reqs := pages.Requests(); len(reqs) != 2 {
		t.Errorf("requests = %v, want the page read again by the refresh", reqs)
	}
}

func TestRefreshOfALinkWhosePageCannotBeReadKeepsTheDocument(t *testing.T) {
	pages := fetchtest.NewMap()
	pages.Set("https://example.com/about", pageEntry("https://example.com/about", "text/html", aboutPage))
	ts, _ := newLinkServer(t, pages)
	doc, _ := addLink(t, ts, "https://example.com/about")
	before, _ := docText(t, ts, doc["id"])

	pages.Set("https://example.com/about", fetchtest.MapEntry{Err: fetch.ErrNotFound})
	out, status := refreshLink(t, ts, doc["id"])
	if status != http.StatusUnprocessableEntity || out["error"] != "The page was not found." {
		t.Errorf("status %d, body %v; want 422 saying the page was not found", status, out)
	}
	if after, _ := docText(t, ts, doc["id"]); after != before {
		t.Errorf("text changed to %q after a failed refresh", after)
	}
}

func TestRefreshIsRefusedWhileTheDocumentIsBeingAnalysed(t *testing.T) {
	pages := fetchtest.NewMap()
	pages.Set("https://example.com/about", pageEntry("https://example.com/about", "text/html", aboutPage))
	ts, d := newLinkServer(t, pages)
	doc, _ := addLink(t, ts, "https://example.com/about")
	d.Exec(`UPDATE document SET state='analysing' WHERE id=?`, doc["id"])

	if out, status := refreshLink(t, ts, doc["id"]); status != http.StatusConflict {
		t.Errorf("status %d, body %v; want 409", status, out)
	}
	if reqs := pages.Requests(); len(reqs) != 1 {
		t.Errorf("requests = %v, want the page not read while it is being analysed", reqs)
	}
}
