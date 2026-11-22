package source_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/source"
)

func listGeneric(t *testing.T, m *fetchtest.Map, addr string) ([]listing.Posting, error) {
	t.Helper()
	u, err := url.Parse(addr)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	mod, site := source.For(u)
	return mod.List(context.Background(), m, site)
}

func TestList_jsonld_single_maps_all_fields(t *testing.T) {
	body := mustReadFile(t, "testdata/jsonld_single.html")
	addr := "https://jobs.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, ContentType: "text/html", Body: body}})
	ps, err := listGeneric(t, m, addr)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d postings, want 1", len(ps))
	}
	p := ps[0]
	if p.Title != "Backend Engineer" {
		t.Errorf("title = %q", p.Title)
	}
	if p.Company != "Acme Corp" {
		t.Errorf("company = %q", p.Company)
	}
	if p.URL != "https://jobs.example.com/backend-123" {
		t.Errorf("url = %q", p.URL)
	}
	if !strings.Contains(p.Location, "Remote") {
		t.Errorf("location %q missing Remote", p.Location)
	}
	if !strings.Contains(p.Location, "Berlin") {
		t.Errorf("location %q missing Berlin", p.Location)
	}
	wantDate := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	if !p.Published.Equal(wantDate) {
		t.Errorf("published = %v, want %v", p.Published, wantDate)
	}
	if p.PublishedBasis != "datePosted" {
		t.Errorf("publishedBasis = %q, want datePosted", p.PublishedBasis)
	}
	if p.Fields["employment_type"] != "FULL_TIME" {
		t.Errorf("employment_type = %q", p.Fields["employment_type"])
	}
	if p.Fields["salary"] == "" {
		t.Errorf("salary field empty")
	}
	if p.Text == "" {
		t.Errorf("text is empty")
	}
	if p.TextState != listing.TextFull {
		t.Errorf("text state = %q, want full", p.TextState)
	}
	if !p.Expires.IsZero() == false {
		t.Errorf("expires should be set")
	}
}

func TestList_jsonld_array_returns_all(t *testing.T) {
	body := mustReadFile(t, "testdata/jsonld_array.html")
	addr := "https://jobs.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	ps, err := listGeneric(t, m, addr)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d postings, want 2", len(ps))
	}
	if ps[0].Title != "Frontend Dev" {
		t.Errorf("first title = %q", ps[0].Title)
	}
	if ps[1].Title != "Data Analyst" {
		t.Errorf("second title = %q", ps[1].Title)
	}
}

func TestList_jsonld_graph_finds_posting(t *testing.T) {
	body := mustReadFile(t, "testdata/jsonld_graph.html")
	addr := "https://jobs.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 1 {
		t.Fatalf("List err=%v, len=%d, want 1", err, len(ps))
	}
	if ps[0].Title != "DevOps Engineer" {
		t.Errorf("title = %q", ps[0].Title)
	}
}

func TestList_jsonld_no_url_single_takes_page_address(t *testing.T) {
	const body = `<html><head><script type="application/ld+json">
{"@type":"JobPosting","title":"Analyst","hiringOrganization":{"name":"Acme"},"description":"<p>text</p>"}
</script></head></html>`
	addr := "https://board.example.com/job/42"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: []byte(body)}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 1 {
		t.Fatalf("List err=%v len=%d", err, len(ps))
	}
	if ps[0].URL != addr {
		t.Errorf("url = %q, want page address %q", ps[0].URL, addr)
	}
}

func TestList_jsonld_no_url_multiple_dropped(t *testing.T) {
	const body = `<html><head><script type="application/ld+json">
[
  {"@type":"JobPosting","title":"A","hiringOrganization":{"name":"C"}},
  {"@type":"JobPosting","title":"B","hiringOrganization":{"name":"C"}}
]
</script></head></html>`
	addr := "https://board.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: []byte(body)}})
	_, err := listGeneric(t, m, addr)
	if err != source.ErrNothingRecognised {
		t.Errorf("error = %v, want ErrNothingRecognised (items without url dropped)", err)
	}
}

func TestList_getro_next_data(t *testing.T) {
	body := mustReadFile(t, "testdata/getro.html")
	addr := "https://portfolio.example.com/jobs"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 1 {
		t.Fatalf("List err=%v len=%d", err, len(ps))
	}
	p := ps[0]
	if p.Title != "Smart Contract Dev" {
		t.Errorf("title = %q", p.Title)
	}
	if p.Company != "DeFi Protocol" {
		t.Errorf("company = %q", p.Company)
	}
	if p.ApplyURL == "" {
		t.Errorf("ApplyURL empty, want ATS link")
	}
	if p.Location != "Lisbon" {
		t.Errorf("location = %q, want Lisbon (from locationDetails)", p.Location)
	}
	if p.Fields["workplace_type"] != "on_site" {
		t.Errorf("workplace_type = %q, want on_site", p.Fields["workplace_type"])
	}
	if p.Fields["salary"] != "USD 80000–120000/year" {
		t.Errorf("salary = %q, want USD 80000–120000/year (from cents)", p.Fields["salary"])
	}
	if p.TextState != listing.TextNotFetched {
		t.Errorf("text state = %q, want not_fetched", p.TextState)
	}
}

func TestList_links_heuristic(t *testing.T) {
	body := mustReadFile(t, "testdata/links_heuristic.html")
	addr := "https://startup.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 2 {
		t.Fatalf("List err=%v len=%d, want 2 vacancy links", err, len(ps))
	}
	if ps[0].Title != "Senior Engineer" {
		t.Errorf("first title = %q", ps[0].Title)
	}
	if ps[0].TextState != listing.TextNotFetched {
		t.Errorf("text state = %q", ps[0].TextState)
	}
}

func TestList_links_pattern_web3career_config(t *testing.T) {
	body := mustReadFile(t, "testdata/links_pattern.html")
	addr := "https://web3.career/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	u := mustParseURL(t, addr)
	mod, site := source.For(u)
	ps, err := mod.List(context.Background(), m, site)
	if err != nil || len(ps) != 2 {
		t.Fatalf("List err=%v len=%d, want 2", err, len(ps))
	}
	if ps[0].Title != "Solidity Developer" {
		t.Errorf("first title = %q", ps[0].Title)
	}
}

func TestList_a16z_portfolio_jobs(t *testing.T) {
	body := mustReadFile(t, "testdata/a16z.html")
	addr := "https://a16zfrontier.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	u := mustParseURL(t, addr)
	mod, site := source.For(u)
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) != 3 {
		t.Fatalf("got %d postings, want 3", len(ps))
	}
	if ps[0].Company != "Decentral Labs" {
		t.Errorf("company[0] = %q", ps[0].Company)
	}
	if ps[0].Location != "New York" {
		t.Errorf("location[0] = %q, want New York", ps[0].Location)
	}
	if ps[0].Fields["salary"] != "USD 100000–150000/year" {
		t.Errorf("salary[0] = %q, want USD 100000–150000/year", ps[0].Fields["salary"])
	}
	if ps[0].Fields["workplace_type"] != "" {
		t.Errorf("workplace_type[0] = %q, want empty (remote=false)", ps[0].Fields["workplace_type"])
	}
	if ps[1].Company != "Chain Works" {
		t.Errorf("company[1] = %q", ps[1].Company)
	}
	if ps[1].Location != "" {
		t.Errorf("location[1] = %q, want empty (locations is empty)", ps[1].Location)
	}
	if ps[1].Fields["workplace_type"] != "remote" {
		t.Errorf("workplace_type[1] = %q, want remote", ps[1].Fields["workplace_type"])
	}
	if reqs := m.Requests(); len(reqs) != 1 {
		t.Errorf("requests = %v, want exactly 1", reqs)
	}
}

func TestList_site_config_uses_list_url(t *testing.T) {
	feedBody := mustReadFile(t, "testdata/rss.xml")
	getroBody := mustReadFile(t, "testdata/getro.html")
	for _, tc := range []struct {
		site string
		feed string
		body []byte
		ct   string
	}{
		{"https://remote3.co/", "https://remote3.co/api/rss", feedBody, "application/rss+xml"},
		{"https://www.remote3.co/", "https://remote3.co/api/rss", feedBody, "application/rss+xml"},
		{"https://weworkremotely.com/", "https://weworkremotely.com/remote-jobs.rss", feedBody, "application/rss+xml"},
		{"https://cryptocurrencyjobs.co/", "https://cryptocurrencyjobs.co/index.xml", feedBody, "application/rss+xml"},
		{"https://jobs.framework.ventures/", "https://jobs.framework.ventures/jobs", getroBody, "text/html"},
	} {
		t.Run(tc.site, func(t *testing.T) {
			m := fetchtest.NewMap()
			m.Set(tc.feed, fetchtest.MapEntry{Response: fetch.Response{URL: tc.feed, Status: 200, ContentType: tc.ct, Body: tc.body}})
			u := mustParseURL(t, tc.site)
			mod, site := source.For(u)
			if _, err := mod.List(context.Background(), m, site); err != nil {
				t.Fatalf("List: %v", err)
			}
			if reqs := m.Requests(); len(reqs) != 1 || reqs[0] != tc.feed {
				t.Errorf("requested %v, want [%s]", reqs, tc.feed)
			}
		})
	}
}

func TestList_web3career_skips_jsonld_uses_link_pattern(t *testing.T) {
	body := mustReadFile(t, "testdata/links_pattern.html")
	addr := "https://web3.careers/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	u := mustParseURL(t, addr)
	mod, site := source.For(u)
	ps, err := mod.List(context.Background(), m, site)
	if err != nil || len(ps) == 0 {
		t.Fatalf("List err=%v len=%d", err, len(ps))
	}
	if reqs := m.Requests(); len(reqs) != 1 {
		t.Errorf("requests = %v, want exactly 1", reqs)
	}
}

func TestList_skipJSONLD_falls_to_links(t *testing.T) {
	const body = `<html><head><script type="application/ld+json">
[{"@type":"JobPosting","title":"Analyst","url":"https://x.com/j/1","hiringOrganization":{"name":"Acme"}}]
</script></head><body>
<a href="/jobs/analyst-123">Analyst</a>
</body></html>`
	addr := "https://skipme.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: []byte(body)}})
	u := mustParseURL(t, addr)
	mod, site := source.For(u)
	site.SkipJSONLD = true
	ps, err := mod.List(context.Background(), m, site)
	if err != nil || len(ps) == 0 {
		t.Fatalf("List err=%v len=%d, want links", err, len(ps))
	}
	if ps[0].Title != "Analyst" {
		t.Errorf("title = %q (should be from link text)", ps[0].Title)
	}
}

func TestList_generic_one_request(t *testing.T) {
	body := mustReadFile(t, "testdata/jsonld_single.html")
	addr := "https://jobs.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	listGeneric(t, m, addr)
	if reqs := m.Requests(); len(reqs) != 1 {
		t.Errorf("requests = %v, want exactly 1", reqs)
	}
}

func TestDetail_match_by_address(t *testing.T) {
	body := mustReadFile(t, "testdata/detail_jsonld.html")
	pageURL := "https://careers.example.com/jobs/proto-42"
	m := fetchtest.NewMap()
	m.Set(pageURL, fetchtest.MapEntry{Response: fetch.Response{URL: pageURL, Status: 200, Body: body}})
	p := listing.Posting{URL: pageURL, Title: "Protocol Engineer", Company: "Layer Protocol"}
	result, err := source.Detail(context.Background(), m, p, source.Site{Address: pageURL})
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if result.TextState != listing.TextFull {
		t.Errorf("text state = %q, want full", result.TextState)
	}
	if !strings.Contains(result.Text, "protocol") {
		t.Errorf("text %q does not contain expected content", result.Text)
	}
	if result.Fields["employment_type"] != "FULL_TIME" {
		t.Errorf("employment_type = %q", result.Fields["employment_type"])
	}
	if result.ApplyURL == "" {
		t.Errorf("ApplyURL empty, want ATS link from page")
	}
}

func TestDetail_match_by_title_and_company(t *testing.T) {
	body := mustReadFile(t, "testdata/detail_jsonld.html")
	pageURL := "https://careers.example.com/jobs/proto-42"
	altURL := "https://aggregator.example.com/post/999"
	m := fetchtest.NewMap()
	m.Set(altURL, fetchtest.MapEntry{Response: fetch.Response{URL: pageURL, Status: 200, Body: body}})
	p := listing.Posting{URL: altURL, Title: "Protocol Engineer", Company: "Layer Protocol"}
	result, err := source.Detail(context.Background(), m, p, source.Site{Address: altURL})
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if result.TextState != listing.TextFull {
		t.Errorf("text state = %q, want full (matched by title+company)", result.TextState)
	}
}

func TestDetail_ambiguous_page_no_text(t *testing.T) {
	const body = `<html><head><script type="application/ld+json">
[
  {"@type":"JobPosting","title":"Staff Engineer","url":"https://jobs.example.com/se-1","hiringOrganization":{"name":"Acme"},"description":"<p>role 1</p>"},
  {"@type":"JobPosting","title":"Staff Engineer","url":"https://jobs.example.com/se-2","hiringOrganization":{"name":"Acme"},"description":"<p>role 2</p>"}
]
</script></head></html>`
	pageURL := "https://aggregator.example.com/redirect/42"
	m := fetchtest.NewMap()
	m.Set(pageURL, fetchtest.MapEntry{Response: fetch.Response{URL: pageURL, Status: 200, Body: []byte(body)}})
	p := listing.Posting{URL: pageURL, Title: "Staff Engineer", Company: "Acme"}
	result, err := source.Detail(context.Background(), m, p, source.Site{Address: pageURL})
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if result.Text != "" {
		t.Errorf("text should be empty for ambiguous page, got %q", result.Text)
	}
}

func TestDetail_body_fallback_main_element(t *testing.T) {
	body := mustReadFile(t, "testdata/detail_body.html")
	pageURL := "https://startup.example.com/jobs/auditor"
	m := fetchtest.NewMap()
	m.Set(pageURL, fetchtest.MapEntry{Response: fetch.Response{URL: pageURL, Status: 200, Body: body}})
	p := listing.Posting{URL: pageURL, Title: "Security Auditor", Company: "Startup"}
	result, err := source.Detail(context.Background(), m, p, source.Site{Address: pageURL})
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if result.TextState != listing.TextFull {
		t.Errorf("text state = %q, want full", result.TextState)
	}
	if !strings.Contains(result.Text, "auditor") {
		t.Errorf("text %q missing expected content", result.Text)
	}
}

func TestDetail_content_selector(t *testing.T) {
	body := mustReadFile(t, "testdata/detail_content.html")
	pageURL := "https://web3.career/solidity-dev/123456"
	m := fetchtest.NewMap()
	m.Set(pageURL, fetchtest.MapEntry{Response: fetch.Response{URL: pageURL, Status: 200, Body: body}})
	cfg := source.Site{
		Address:    pageURL,
		SkipJSONLD: true,
		Content:    []source.Attr{{Attr: "class", Substr: "text-dark-grey-text"}},
	}
	p := listing.Posting{URL: pageURL, Title: "Solidity Dev", Company: ""}
	result, err := source.Detail(context.Background(), m, p, cfg)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if result.TextState != listing.TextFull || strings.Contains(result.Text, "Not the description") {
		t.Errorf("text state=%q or includes chrome, want full and content only", result.TextState)
	}
	if !strings.Contains(result.Text, "smart contracts") {
		t.Errorf("text %q missing expected content", result.Text)
	}
	if result.Company != "BlockCo" || result.Location != "Remote" {
		t.Errorf("company=%q location=%q, want BlockCo and Remote from JSON-LD fields", result.Company, result.Location)
	}
}

func TestDetail_ats_link_becomes_apply_url(t *testing.T) {
	const body = `<html><body>
<p>Apply here: <a href="https://jobs.ashbyhq.com/coolco/abc-def-123">Apply</a></p>
</body></html>`
	pageURL := "https://aggregator.example.com/post/5"
	m := fetchtest.NewMap()
	m.Set(pageURL, fetchtest.MapEntry{Response: fetch.Response{URL: pageURL, Status: 200, Body: []byte(body)}})
	p := listing.Posting{URL: pageURL, Title: "Engineer", Company: "CoolCo"}
	result, err := source.Detail(context.Background(), m, p, source.Site{Address: pageURL})
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if result.ApplyURL == "" {
		t.Errorf("ApplyURL empty, want ATS link")
	}
	if !strings.Contains(result.ApplyURL, "ashbyhq.com") {
		t.Errorf("ApplyURL = %q, want ashbyhq.com link", result.ApplyURL)
	}
}

func TestDetail_fetch_error_returned(t *testing.T) {
	m := fetchtest.NewMap()
	m.Set("https://blocked.example.com/job/1", fetchtest.MapEntry{Err: fetch.ErrBlocked})
	p := listing.Posting{URL: "https://blocked.example.com/job/1", Title: "Engineer"}
	_, err := source.Detail(context.Background(), m, p, source.Site{Address: "https://blocked.example.com/job/1"})
	if err != fetch.ErrBlocked {
		t.Errorf("error = %v, want ErrBlocked", err)
	}
}

func TestDetail_one_request(t *testing.T) {
	body := mustReadFile(t, "testdata/detail_body.html")
	pageURL := "https://startup.example.com/jobs/auditor"
	m := fetchtest.NewMap()
	m.Set(pageURL, fetchtest.MapEntry{Response: fetch.Response{URL: pageURL, Status: 200, Body: body}})
	p := listing.Posting{URL: pageURL, Title: "Auditor"}
	source.Detail(context.Background(), m, p, source.Site{Address: pageURL})
	if reqs := m.Requests(); len(reqs) != 1 {
		t.Errorf("requests = %v, want exactly 1", reqs)
	}
}

func TestList_links_title_extraction(t *testing.T) {
	addr := "https://board.example.com/"
	for _, tc := range []struct {
		name    string
		html    string
		title   string
		company string
	}{
		{
			name:  "aria-label en-dash splits company and title",
			html:  "<html><body><a href=\"/jobs/analyst-123\" aria-label=\"Acme Corp – Data Analyst\">x</a></body></html>",
			title: "Data Analyst", company: "Acme Corp",
		},
		{
			name:  "h3 heading collapsed as title; first heading wins",
			html:  "<html><body><a href=\"/jobs/dev-456\"><h3>  Senior   Engineer  </h3><h3>Other Heading</h3></a></body></html>",
			title: "Senior Engineer",
		},
		{
			name:  "first text span is title when multiple spans",
			html:  "<html><body><a href=\"/jobs/dev-789\"><span>Full-Stack Developer</span><span>•</span><span>Widget Co</span></a></body></html>",
			title: "Full-Stack Developer",
		},
		{
			name:  "signup and login paths skipped",
			html:  "<html><body><a href=\"/jobs/analyst-789\">Analyst</a><a href=\"/signup\">Join</a><a href=\"/jobs/login\">Login</a></body></html>",
			title: "Analyst",
		},
		{
			name:  "apply and view title prefixes skipped",
			html:  "<html><body><a href=\"/jobs/eng-101\">Engineer</a><a href=\"/jobs/apply-link\">Apply for this job</a><a href=\"/jobs/view-more\">View all roles</a></body></html>",
			title: "Engineer",
		},
		{
			name:  "second link to same address supplies company from h3",
			html:  `<html><body><a href="/career/engineer-acme/12345"><h2>Acme Engineer</h2></a><a href="/career/engineer-acme/12345"><h3>Acme</h3></a></body></html>`,
			title: "Acme Engineer", company: "Acme",
		},
		{
			name:  "second link with h3 does not replace existing company",
			html:  `<html><body><a href="/jobs/dev-999" aria-label="Zeta Corp – Dev">x</a><a href="/jobs/dev-999"><h3>Other Corp</h3></a></body></html>`,
			title: "Dev", company: "Zeta Corp",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fetchtest.NewMap()
			m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: []byte(tc.html)}})
			ps, err := listGeneric(t, m, addr)
			if err != nil || len(ps) != 1 {
				t.Fatalf("List err=%v len=%d, want 1", err, len(ps))
			}
			if tc.title != "" && ps[0].Title != tc.title {
				t.Errorf("title = %q, want %q", ps[0].Title, tc.title)
			}
			if tc.company != "" && ps[0].Company != tc.company {
				t.Errorf("company = %q, want %q", ps[0].Company, tc.company)
			}
		})
	}
}

func TestList_generic_prefers_jsonld_over_links(t *testing.T) {
	const body = `<html><head><script type="application/ld+json">
{"@type":"JobPosting","title":"Structured Engineer","url":"https://board.example.com/jobs/structured","hiringOrganization":{"name":"Acme"}}
</script></head><body><a href="https://board.example.com/jobs/linked"><h3>Linked Engineer</h3></a></body></html>`
	addr := "https://board.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, ContentType: "text/html", Body: []byte(body)}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 1 || ps[0].Title != "Structured Engineer" {
		t.Fatalf("List err=%v postings=%+v, want the JSON-LD posting only", err, ps)
	}
}

func TestList_generic_xml_that_is_not_a_feed_falls_to_links(t *testing.T) {
	const body = `<?xml version="1.0"?><html><body><a href="https://board.example.com/jobs/linked"><h3>Linked Engineer</h3></a></body></html>`
	addr := "https://board.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, ContentType: "application/xml", Body: []byte(body)}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 1 || ps[0].Title != "Linked Engineer" {
		t.Fatalf("List err=%v postings=%+v, want the link posting", err, ps)
	}
}
