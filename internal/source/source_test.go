package source_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/listing"
	"github.com/sifty-works/sifty-server/internal/source"
)

func TestConcreteATS(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{"https://boards.greenhouse.io/acme/jobs/12345", "https://boards.greenhouse.io/acme/jobs/12345", true},
		{"https://job-boards.greenhouse.io/acme/jobs/12345", "https://job-boards.greenhouse.io/acme/jobs/12345", true},
		{"https://boards.greenhouse.io/acme/jobs/12345/apply", "https://boards.greenhouse.io/acme/jobs/12345", true},
		{"https://jobs.lever.co/acme/550e8400-e29b-41d4-a716-446655440000", "https://jobs.lever.co/acme/550e8400-e29b-41d4-a716-446655440000", true},
		{"https://jobs.lever.co/acme/550e8400-e29b-41d4-a716-446655440000/apply", "https://jobs.lever.co/acme/550e8400-e29b-41d4-a716-446655440000", true},
		{"https://jobs.ashbyhq.com/acme/550e8400-e29b-41d4-a716-446655440000", "https://jobs.ashbyhq.com/acme/550e8400-e29b-41d4-a716-446655440000", true},
		{"https://jobs.ashbyhq.com/acme/550e8400-e29b-41d4-a716-446655440000/apply", "https://jobs.ashbyhq.com/acme/550e8400-e29b-41d4-a716-446655440000", true},
		{"https://jobs.ashbyhq.com/acme/550e8400-e29b-41d4-a716-446655440000/application", "https://jobs.ashbyhq.com/acme/550e8400-e29b-41d4-a716-446655440000", true},
		{"https://apply.workable.com/acme/j/ABC123", "https://apply.workable.com/acme/j/ABC123", true},
		{"https://apply.workable.com/acme/j/ABC123/apply", "https://apply.workable.com/acme/j/ABC123", true},
		{"https://boards.eu.greenhouse.io/acme/jobs/12345/apply", "https://boards.eu.greenhouse.io/acme/jobs/12345", true},
		{"https://boards.greenhouse.io/acme", "", false},
		{"https://job-boards.greenhouse.io/acme", "", false},
		{"https://jobs.lever.co/acme", "", false},
		{"https://jobs.ashbyhq.com/acme", "", false},
		{"https://apply.workable.com/acme", "", false},
		{"https://apply.workable.com/acme/j", "", false},
		{"https://jobs.eu.lever.co/acme/550e8400-e29b-41d4-a716-446655440000/apply", "https://jobs.eu.lever.co/acme/550e8400-e29b-41d4-a716-446655440000", true},
		{"https://example.com/jobs/42", "", false},
		{"https://linkedin.com/jobs/view/12345", "", false},
	} {
		got, ok := source.ConcreteATS(tc.raw)
		if ok != tc.wantOK {
			t.Errorf("ConcreteATS(%q) ok=%v, want %v", tc.raw, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("ConcreteATS(%q): got %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestFor_module_choice(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want string
	}{
		{"https://boards.greenhouse.io/acme", "greenhouse"},
		{"https://web3.career/", "links"},
		{"https://unknown.example.com/jobs", "generic"},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			u := mustParseURL(t, tc.addr)
			mod, _ := source.For(u)
			if mod.Name() != tc.want {
				t.Errorf("For(%q).Name() = %q, want %q", tc.addr, mod.Name(), tc.want)
			}
		})
	}
}

func TestFor_routing(t *testing.T) {
	cases := []struct {
		name   string
		addr   string
		apiURL string
		body   string
	}{
		{"Greenhouse board", "https://boards.greenhouse.io/acme", "https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true", `{"company":{"name":"Acme"},"jobs":[]}`},
		{"Greenhouse board trailing slash", "https://boards.greenhouse.io/acme/", "https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true", `{"company":{"name":"Acme"},"jobs":[]}`},
		{"Greenhouse job-boards", "https://job-boards.greenhouse.io/acme", "https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true", `{"company":{"name":"Acme"},"jobs":[]}`},
		{"Greenhouse EU", "https://boards.eu.greenhouse.io/acme", "https://boards-api.eu.greenhouse.io/v1/boards/acme/jobs?content=true", `{"company":{"name":"Acme"},"jobs":[]}`},
		{"Greenhouse job-boards EU", "https://job-boards.eu.greenhouse.io/acme", "https://boards-api.eu.greenhouse.io/v1/boards/acme/jobs?content=true", `{"company":{"name":"Acme"},"jobs":[]}`},
		{"Greenhouse vacancy page falls to feed", "https://boards.greenhouse.io/acme/jobs/1234", "https://boards.greenhouse.io/acme/jobs/1234", "<html></html>"},
		{"Lever board", "https://jobs.lever.co/techstartup", "https://api.lever.co/v0/postings/techstartup?mode=json", "[]"},
		{"Lever EU", "https://jobs.eu.lever.co/techstartup", "https://api.eu.lever.co/v0/postings/techstartup?mode=json", "[]"},
		{"Ashby board", "https://jobs.ashbyhq.com/nexgen", "https://api.ashbyhq.com/posting-api/job-board/nexgen?includeCompensation=true", `{"organization":{"name":""},"jobs":[]}`},
		{"other URL falls to feed", "https://weworkremotely.com/remote-jobs.rss", "https://weworkremotely.com/remote-jobs.rss", `<rss version="2.0"><channel></channel></rss>`},
		{"arbitrary URL falls to feed", "https://example.com/jobs/feed.xml", "https://example.com/jobs/feed.xml", `<rss version="2.0"><channel></channel></rss>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fetchtest.NewMap()
			m.Set(tc.apiURL, fetchtest.MapEntry{
				Response: fetch.Response{URL: tc.apiURL, Status: 200, Body: []byte(tc.body)},
			})
			mod, site := source.For(mustParseURL(t, tc.addr))
			mod.List(context.Background(), m, site)
			reqs := m.Requests()
			if len(reqs) != 1 || reqs[0] != tc.apiURL {
				t.Errorf("requested %v, want [%s]", reqs, tc.apiURL)
			}
		})
	}
}

func TestList_unrecognised_format(t *testing.T) {
	cases := []struct {
		name   string
		addr   string
		apiURL string
		body   string
	}{
		{"feed HTML page", "http://example.com/", "http://example.com/", "<!DOCTYPE html><html><body>Jobs</body></html>"},
		{"Greenhouse invalid JSON", "https://boards.greenhouse.io/acme", "https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true", "<!doctype html>"},
		{"Lever invalid JSON", "https://jobs.lever.co/acme", "https://api.lever.co/v0/postings/acme?mode=json", "{not json}"},
		{"Ashby invalid JSON", "https://jobs.ashbyhq.com/acme", "https://api.ashbyhq.com/posting-api/job-board/acme?includeCompensation=true", "not json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fetchtest.NewMap()
			m.Set(tc.apiURL, fetchtest.MapEntry{
				Response: fetch.Response{URL: tc.apiURL, Status: 200, Body: []byte(tc.body)},
			})
			mod, site := source.For(mustParseURL(t, tc.addr))
			_, err := mod.List(context.Background(), m, site)
			if err != source.ErrNothingRecognised {
				t.Errorf("error = %v, want ErrNothingRecognised", err)
			}
		})
	}
}

func TestList_fetcher_error_returned(t *testing.T) {
	m := fetchtest.NewMap()
	m.Set("http://blocked.example.com/feed", fetchtest.MapEntry{Err: fetch.ErrBlocked})
	mod, site := source.For(mustParseURL(t, "http://blocked.example.com/feed"))
	_, err := mod.List(context.Background(), m, site)
	if err != fetch.ErrBlocked {
		t.Errorf("error = %v, want ErrBlocked", err)
	}
}

func TestFeed_RSS_maps_all_fields(t *testing.T) {
	body := mustReadFile(t, "testdata/rss.xml")
	feedURL := "http://feeds.acme.example.com/jobs"
	m := fetchtest.NewMap()
	m.Set(feedURL, fetchtest.MapEntry{
		Response: fetch.Response{URL: feedURL, Status: 200, ContentType: "application/rss+xml", Body: body},
	})
	mod, site := source.For(mustParseURL(t, feedURL))
	site.Name = "Acme Jobs"
	postings, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(postings))
	}

	p := postings[0]
	if p.Title != "Senior Developer" {
		t.Errorf("title = %q, want %q", p.Title, "Senior Developer")
	}
	if p.Company != "Acme" {
		t.Errorf("company = %q, want %q", p.Company, "Acme")
	}
	if p.Text != "<p>Senior Developer role at Acme.</p>" {
		t.Errorf("text = %q", p.Text)
	}
	if p.TextState != listing.TextFull {
		t.Errorf("text state = %q, want full", p.TextState)
	}
	if want := []string{"Engineering", "Backend"}; len(p.Tags) != len(want) {
		t.Errorf("tags = %v, want %v", p.Tags, want)
	}
	wantDate := time.Date(2024, 3, 11, 10, 0, 0, 0, time.UTC)
	if !p.Published.Equal(wantDate) {
		t.Errorf("published = %v, want %v", p.Published, wantDate)
	}
	if p.PublishedBasis != "pubDate" {
		t.Errorf("basis = %q, want pubDate", p.PublishedBasis)
	}
	if want := "http://feeds.acme.example.com/post/1"; p.URL != want {
		t.Errorf("URL = %q, want %q (relative link not resolved)", p.URL, want)
	}

	p2 := postings[1]
	if p2.Company != "Example Co" {
		t.Errorf("item2 company = %q, want %q", p2.Company, "Example Co")
	}
	if p2.Title != "Job at Example Co" {
		t.Errorf("item2 title = %q", p2.Title)
	}
}

func TestFeed_Atom_maps_all_fields(t *testing.T) {
	body := mustReadFile(t, "testdata/atom.xml")
	feedURL := "http://jobs.example.com/atom"
	m := fetchtest.NewMap()
	m.Set(feedURL, fetchtest.MapEntry{
		Response: fetch.Response{URL: feedURL, Status: 200, Body: body},
	})
	mod, site := source.For(mustParseURL(t, feedURL))
	postings, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(postings) != 1 {
		t.Fatalf("got %d postings, want 1", len(postings))
	}
	p := postings[0]
	if p.Title != "DevOps Engineer" {
		t.Errorf("title = %q", p.Title)
	}
	if want := "http://jobs.example.com/devops-engineer"; p.URL != want {
		t.Errorf("URL = %q, want %q", p.URL, want)
	}
	if p.Text != "<p>DevOps role at Example Corp.</p>" {
		t.Errorf("text = %q", p.Text)
	}
	wantDate := time.Date(2024, 3, 5, 12, 0, 0, 0, time.UTC)
	if !p.Published.Equal(wantDate) {
		t.Errorf("published = %v, want %v (should use published, not updated)", p.Published, wantDate)
	}
	notUpdated := time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC)
	if p.Published.Equal(notUpdated) {
		t.Errorf("published uses updated date %v; want first published date", notUpdated)
	}
	if p.PublishedBasis != "published" {
		t.Errorf("basis = %q, want published", p.PublishedBasis)
	}
	if want := []string{"Infrastructure", "Cloud"}; len(p.Tags) != len(want) || p.Tags[0] != want[0] {
		t.Errorf("tags = %v, want %v", p.Tags, want)
	}
}

func TestFeed_title_split(t *testing.T) {
	for _, tc := range []struct {
		name    string
		xml     string
		title   string
		company string
	}{
		{
			name:    "RSS Role at Company split at last at",
			xml:     `<rss version="2.0"><channel><item><title>Engineer at Acme</title><link>http://x.example.com/1</link></item></channel></rss>`,
			title:   "Engineer",
			company: "Acme",
		},
		{
			name:    "RSS multiple at uses last",
			xml:     `<rss version="2.0"><channel><item><title>Senior at Acme at Work</title><link>http://x.example.com/2</link></item></channel></rss>`,
			title:   "Senior at Acme",
			company: "Work",
		},
		{
			name:    "RSS no at left whole",
			xml:     `<rss version="2.0"><channel><item><title>Senior Engineer</title><link>http://x.example.com/3</link></item></channel></rss>`,
			title:   "Senior Engineer",
			company: "",
		},
		{
			name:    "Atom Role at Company split",
			xml:     `<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>Designer at Studio</title><link href="http://x.example.com/4"/><published>2024-01-01T00:00:00Z</published></entry></feed>`,
			title:   "Designer",
			company: "Studio",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := "http://x.example.com/feed"
			m := fetchtest.NewMap()
			m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, ContentType: "application/rss+xml", Body: []byte(tc.xml)}})
			mod, site := source.For(mustParseURL(t, addr))
			ps, err := mod.List(context.Background(), m, site)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(ps) != 1 {
				t.Fatalf("got %d postings, want 1", len(ps))
			}
			if ps[0].Title != tc.title {
				t.Errorf("title = %q, want %q", ps[0].Title, tc.title)
			}
			if ps[0].Company != tc.company {
				t.Errorf("company = %q, want %q", ps[0].Company, tc.company)
			}
		})
	}
}

func TestGreenhouse_maps_all_fields(t *testing.T) {
	body := mustReadFile(t, "testdata/greenhouse.json")
	apiURL := "https://boards-api.greenhouse.io/v1/boards/brightway/jobs?content=true"
	m := fetchtest.NewMap()
	m.Set(apiURL, fetchtest.MapEntry{
		Response: fetch.Response{URL: apiURL, Status: 200, Body: body},
	})
	mod, site := source.For(mustParseURL(t, "https://boards.greenhouse.io/brightway"))
	site.Name = "Brightway Software"
	postings, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(postings) != 2 {
		t.Fatalf("got %d postings, want 2", len(postings))
	}

	p := postings[0]
	if p.URL != "https://boards.greenhouse.io/brightway/jobs/1001" {
		t.Errorf("URL = %q", p.URL)
	}
	if p.ApplyURL != "https://boards.greenhouse.io/brightway/jobs/1001" {
		t.Errorf("ApplyURL = %q", p.ApplyURL)
	}
	if p.Title != "Backend Engineer" {
		t.Errorf("title = %q", p.Title)
	}
	if p.Company != "Brightway Corp" {
		t.Errorf("company = %q, want Brightway Corp (from company_name)", p.Company)
	}
	if p.Location != "Remote" {
		t.Errorf("location = %q", p.Location)
	}
	if len(p.Tags) != 1 || p.Tags[0] != "Engineering" {
		t.Errorf("tags = %v, want [Engineering]", p.Tags)
	}
	wantDate := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	if !p.Published.Equal(wantDate) {
		t.Errorf("published = %v, want %v", p.Published, wantDate)
	}
	notUpdated := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
	if p.Published.Equal(notUpdated) {
		t.Errorf("published uses updated_at %v; must use first_published only", notUpdated)
	}
	if p.PublishedBasis != "first_published" {
		t.Errorf("basis = %q, want first_published", p.PublishedBasis)
	}
	if p.Text == "" {
		t.Errorf("text is empty")
	}
	if p.TextState != listing.TextFull {
		t.Errorf("text state = %q, want full", p.TextState)
	}

	if p.CompanyGuessed {
		t.Error("CompanyGuessed should be false when company_name is set")
	}

	p2 := postings[1]
	if p2.Company != "Brightway Software" {
		t.Errorf("item2 company = %q, want Brightway Software (board name fallback)", p2.Company)
	}
	if len(p2.Tags) != 2 {
		t.Errorf("item2 tags = %v, want 2 departments", p2.Tags)
	}
	if !p2.CompanyGuessed {
		t.Error("CompanyGuessed should be true when company_name is absent")
	}
}

func TestLever_maps_all_fields(t *testing.T) {
	body := mustReadFile(t, "testdata/lever.json")
	apiURL := "https://api.lever.co/v0/postings/techstartup?mode=json"
	m := fetchtest.NewMap()
	m.Set(apiURL, fetchtest.MapEntry{
		Response: fetch.Response{URL: apiURL, Status: 200, Body: body},
	})
	mod, site := source.For(mustParseURL(t, "https://jobs.lever.co/techstartup"))
	site.Name = "TechStartup"
	postings, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(postings) != 1 {
		t.Fatalf("got %d postings, want 1", len(postings))
	}
	p := postings[0]
	if p.URL != "https://jobs.lever.co/techstartup/abc-def-123" {
		t.Errorf("URL = %q", p.URL)
	}
	if p.ApplyURL != p.URL {
		t.Errorf("ApplyURL = %q, want same as URL (hostedUrl)", p.ApplyURL)
	}
	if p.Title != "Full Stack Developer" {
		t.Errorf("title = %q", p.Title)
	}
	if p.Company != "TechStartup" {
		t.Errorf("company = %q, want TechStartup (source name)", p.Company)
	}
	if p.Location != "San Francisco · Austin" {
		t.Errorf("location = %q, want all locations joined (allLocations)", p.Location)
	}
	if len(p.Tags) < 2 {
		t.Errorf("tags = %v, want at least team and department", p.Tags)
	}
	if p.Fields["employment_type"] != "Full-time" {
		t.Errorf("employment_type = %q", p.Fields["employment_type"])
	}
	if p.Fields["workplace_type"] != "hybrid" {
		t.Errorf("workplace_type = %q", p.Fields["workplace_type"])
	}
	if p.Fields["salary"] != "USD 120000–160000/year" {
		t.Errorf("salary = %q, want USD 120000–160000/year", p.Fields["salary"])
	}
	wantDate := time.UnixMilli(1709280000000).UTC()
	if !p.Published.Equal(wantDate) {
		t.Errorf("published = %v, want %v", p.Published, wantDate)
	}
	if p.PublishedBasis != "createdAt" {
		t.Errorf("basis = %q, want createdAt", p.PublishedBasis)
	}
	if p.Text == "" {
		t.Errorf("text is empty")
	}
	for _, want := range []string{"<p>We are building great things.</p>", "<h3>Requirements</h3>", "<ul>", "<p>Apply via our website.</p>"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	if p.CompanyGuessed {
		t.Error("CompanyGuessed should be false when source name is set")
	}
}

func TestLever_company_falls_back_to_site_slug(t *testing.T) {
	apiURL := "https://api.lever.co/v0/postings/techstartup?mode=json"
	m := fetchtest.NewMap()
	m.Set(apiURL, fetchtest.MapEntry{
		Response: fetch.Response{URL: apiURL, Status: 200, Body: mustReadFile(t, "testdata/lever.json")},
	})
	mod, site := source.For(mustParseURL(t, "https://jobs.lever.co/techstartup"))
	postings, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(postings) == 0 {
		t.Fatal("no postings")
	}
	if postings[0].Company != "techstartup" {
		t.Errorf("company = %q, want techstartup (site slug)", postings[0].Company)
	}
	if !postings[0].CompanyGuessed {
		t.Error("CompanyGuessed should be true without source name")
	}
}

func TestAshby_maps_all_fields(t *testing.T) {
	body := mustReadFile(t, "testdata/ashby.json")
	apiURL := "https://api.ashbyhq.com/posting-api/job-board/nexgen?includeCompensation=true"
	m := fetchtest.NewMap()
	m.Set(apiURL, fetchtest.MapEntry{
		Response: fetch.Response{URL: apiURL, Status: 200, Body: body},
	})
	mod, site := source.For(mustParseURL(t, "https://jobs.ashbyhq.com/nexgen"))
	site.Name = "NexGen"
	postings, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(postings) != 1 {
		t.Fatalf("got %d postings, want 1 (unlisted skipped)", len(postings))
	}
	p := postings[0]
	if p.URL != "https://jobs.ashbyhq.com/nexgen/xyz-123" {
		t.Errorf("URL = %q", p.URL)
	}
	if p.ApplyURL != "https://jobs.ashbyhq.com/nexgen/xyz-123/application" {
		t.Errorf("ApplyURL = %q", p.ApplyURL)
	}
	if p.Title != "Data Scientist" {
		t.Errorf("title = %q", p.Title)
	}
	if p.Company != "NexGen" {
		t.Errorf("company = %q, want NexGen (source name)", p.Company)
	}
	if !strings.Contains(p.Location, "Austin, TX") || !strings.Contains(p.Location, "New York") {
		t.Errorf("location = %q, want Austin, TX and New York (secondary location)", p.Location)
	}
	if strings.Contains(p.Location, "Remote") {
		t.Errorf("location = %q must not contain Remote (workplace_type carries it)", p.Location)
	}
	if len(p.Tags) < 2 {
		t.Errorf("tags = %v, want department + team", p.Tags)
	}
	if p.Fields["employment_type"] != "FullTime" {
		t.Errorf("employment_type = %q", p.Fields["employment_type"])
	}
	if p.Fields["workplace_type"] != "Remote" {
		t.Errorf("workplace_type = %q, want Remote", p.Fields["workplace_type"])
	}
	if p.Fields["salary"] != "USD 140000–180000/year" {
		t.Errorf("salary = %q, want USD 140000–180000/year (from summaryComponents)", p.Fields["salary"])
	}
	wantDate := time.Date(2024, 3, 10, 10, 0, 0, 0, time.UTC)
	if !p.Published.Equal(wantDate) {
		t.Errorf("published = %v, want %v", p.Published, wantDate)
	}
	if p.PublishedBasis != "publishedAt" {
		t.Errorf("basis = %q, want publishedAt", p.PublishedBasis)
	}
	if p.Text != "<p>Join our AI research team.</p>" {
		t.Errorf("text = %q", p.Text)
	}
	if p.CompanyGuessed {
		t.Error("CompanyGuessed should be false when source name is set")
	}
}

func TestAshby_company_falls_back_to_board_name(t *testing.T) {
	apiURL := "https://api.ashbyhq.com/posting-api/job-board/nexgen?includeCompensation=true"
	m := fetchtest.NewMap()
	m.Set(apiURL, fetchtest.MapEntry{
		Response: fetch.Response{URL: apiURL, Status: 200, Body: mustReadFile(t, "testdata/ashby.json")},
	})
	mod, site := source.For(mustParseURL(t, "https://jobs.ashbyhq.com/nexgen"))
	postings, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(postings) == 0 {
		t.Fatal("no postings")
	}
	if postings[0].Company != "NexGen AI" {
		t.Errorf("company = %q, want NexGen AI (org name from API)", postings[0].Company)
	}
	if !postings[0].CompanyGuessed {
		t.Error("CompanyGuessed should be true without source name")
	}
}

func TestGetro_cents_become_whole_amounts(t *testing.T) {
	body := mustReadFile(t, "testdata/getro.html")
	addr := "https://jobs.framework.ventures/jobs"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	u := mustParseURL(t, "https://jobs.framework.ventures/")
	mod, site := source.For(u)
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) == 0 {
		t.Fatal("no postings")
	}
	p := ps[0]
	if p.Fields["salary"] != "USD 80000–120000/year" {
		t.Errorf("salary = %q, want USD 80000–120000/year (cents / 100)", p.Fields["salary"])
	}
	if p.Fields["workplace_type"] != "on_site" {
		t.Errorf("workplace_type = %q, want on_site", p.Fields["workplace_type"])
	}
	if p.Location != "Lisbon" {
		t.Errorf("location = %q, want Lisbon (from locationDetails)", p.Location)
	}
}

func TestAshby_1_YEAR_interval_gives_per_year(t *testing.T) {
	body := mustReadFile(t, "testdata/ashby.json")
	apiURL := "https://api.ashbyhq.com/posting-api/job-board/nexgen?includeCompensation=true"
	m := fetchtest.NewMap()
	m.Set(apiURL, fetchtest.MapEntry{Response: fetch.Response{URL: apiURL, Status: 200, Body: body}})
	mod, site := source.For(mustParseURL(t, "https://jobs.ashbyhq.com/nexgen"))
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) == 0 {
		t.Fatal("no postings")
	}
	sal := ps[0].Fields["salary"]
	if !strings.Contains(sal, "/year") {
		t.Errorf("salary = %q, want /year period from 1 YEAR interval", sal)
	}
}

func TestLever_keeps_every_location(t *testing.T) {
	body := mustReadFile(t, "testdata/lever.json")
	apiURL := "https://api.lever.co/v0/postings/techstartup?mode=json"
	m := fetchtest.NewMap()
	m.Set(apiURL, fetchtest.MapEntry{Response: fetch.Response{URL: apiURL, Status: 200, Body: body}})
	mod, site := source.For(mustParseURL(t, "https://jobs.lever.co/techstartup"))
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) == 0 {
		t.Fatal("no postings")
	}
	loc := ps[0].Location
	if !strings.Contains(loc, "San Francisco") || !strings.Contains(loc, "Austin") {
		t.Errorf("location = %q, want all allLocations entries", loc)
	}
}

func TestHN_list(t *testing.T) {
	searchURL := "https://hn.algolia.com/api/v1/search_by_date?tags=story,author_whoishiring&hitsPerPage=10"
	storyURL := "https://hn.algolia.com/api/v1/items/42"
	searchBody := `{"hits":[{"objectID":"99","title":"Ask HN: Who wants to be hired? (Jan 2026)"},{"objectID":"42","title":"Ask HN: Who is hiring? (Jan 2026)"}]}`
	storyBody := `{"id":42,"children":[
		{"id":101,"text":"Acme Corp | <a href=\"https:&#x2F;&#x2F;acme.example.com\">https:&#x2F;&#x2F;acme.example.com</a> | acme.example | Engineer &amp; Lead | Berlin<p>More text","created_at":"2026-01-01T00:00:00Z","children":[]},
		{"id":102,"text":"No pipes here","created_at":"2026-01-02T00:00:00Z","children":[]},
		{"id":103,"text":"","created_at":"2026-01-03T00:00:00Z","children":[]},
		{"id":104,"text":"Zeta Inc | Remote | Contract","created_at":"2026-01-04T00:00:00Z","children":[]}
	]}`
	m := fetchtest.NewMap()
	m.Set(searchURL, fetchtest.MapEntry{Response: fetch.Response{URL: searchURL, Status: 200, Body: []byte(searchBody)}})
	m.Set(storyURL, fetchtest.MapEntry{Response: fetch.Response{URL: storyURL, Status: 200, Body: []byte(storyBody)}})
	mod, site := source.For(mustParseURL(t, "https://news.ycombinator.com/"))
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d postings, want 2 (no-pipe and deleted skipped; no-title now falls back)", len(ps))
	}
	if ps[0].Company != "Acme Corp" {
		t.Errorf("company = %q", ps[0].Company)
	}
	if ps[0].Title != "Engineer & Lead" {
		t.Errorf("title = %q", ps[0].Title)
	}
	if ps[0].Location != "Berlin" {
		t.Errorf("location = %q, want Berlin", ps[0].Location)
	}
	if reqs := m.Requests(); len(reqs) != 2 {
		t.Errorf("requests = %v, want exactly 2", reqs)
	}
	if !strings.Contains(ps[0].URL, "101") {
		t.Errorf("URL = %q, want comment id 101", ps[0].URL)
	}
}

func TestHN_fields_from_tokens(t *testing.T) {
	searchURL := "https://hn.algolia.com/api/v1/search_by_date?tags=story,author_whoishiring&hitsPerPage=10"
	storyURL := "https://hn.algolia.com/api/v1/items/55"
	searchBody := `{"hits":[{"objectID":"55","title":"Ask HN: Who is hiring? (Feb 2026)"}]}`
	storyBody := `{"id":55,"children":[
		{"id":201,"text":"Zeta Corp | Software Engineer | REMOTE (US) | Full-time | $90k-120k<p>Details","created_at":"2026-02-01T00:00:00Z","children":[]}
	]}`
	m := fetchtest.NewMap()
	m.Set(searchURL, fetchtest.MapEntry{Response: fetch.Response{URL: searchURL, Status: 200, Body: []byte(searchBody)}})
	m.Set(storyURL, fetchtest.MapEntry{Response: fetch.Response{URL: storyURL, Status: 200, Body: []byte(storyBody)}})
	mod, site := source.For(mustParseURL(t, "https://news.ycombinator.com/"))
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d postings, want 1", len(ps))
	}
	p := ps[0]
	if p.Title != "Software Engineer" {
		t.Errorf("title = %q", p.Title)
	}
	if p.Location != "US" {
		t.Errorf("location = %q, want US (from REMOTE parenthetical)", p.Location)
	}
	if p.Fields["workplace_type"] == "" {
		t.Errorf("workplace_type empty, want remote token")
	}
	if p.Fields["employment_type"] != "Full-time" {
		t.Errorf("employment_type = %q, want Full-time", p.Fields["employment_type"])
	}
	if p.Fields["salary"] != "$90k-120k" {
		t.Errorf("salary = %q, want $90k-120k", p.Fields["salary"])
	}
}

func hnFixture(t *testing.T, stories map[string]string, search string) []listing.Posting {
	t.Helper()
	m := fetchtest.NewMap()
	searchURL := "https://hn.algolia.com/api/v1/search_by_date?tags=story,author_whoishiring&hitsPerPage=10"
	m.Set(searchURL, fetchtest.MapEntry{Response: fetch.Response{URL: searchURL, Status: 200, Body: []byte(search)}})
	for id, body := range stories {
		u := "https://hn.algolia.com/api/v1/items/" + id
		m.Set(u, fetchtest.MapEntry{Response: fetch.Response{URL: u, Status: 200, Body: []byte(body)}})
	}
	mod, site := source.For(mustParseURL(t, "https://news.ycombinator.com/"))
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return ps
}

func TestHN_header_shapes(t *testing.T) {
	cases := []struct {
		name, header                         string
		company, title, location, work, empl string
	}{
		{"place and mode parts are not the role", "ACME | Springfield, IL and Berlin, Germany | Full-time / Remote | ONSITE | https://acme.example/", "ACME", "Open roles", "Springfield, IL and Berlin, Germany", "Remote", "Full-time"},
		{"work mode combination is not the role", "Beta Inc | REMOTE or HYBRID | Python developer", "Beta Inc", "Python developer", "", "REMOTE / HYBRID", ""},
		{"place without a comma is not the role", "Foo Labs | Berlin | Product Engineer", "Foo Labs", "Product Engineer", "Berlin", "", ""},
		{"work mode in the role part is still read", "Gamma | Remote Backend Engineer | https://gamma.example", "Gamma", "Remote Backend Engineer", "", "Remote", ""},
		{"a plain part after the company is the location, not the role", "Pond AI | San Francisco", "Pond AI", "Open roles", "San Francisco", "", ""},
		{"a part starting with a work mode gives mode and place", "Delta | ONSITE in SF | Software Engineer", "Delta", "Software Engineer", "SF", "ONSITE", ""},
		{"a parenthetical work mode gives mode and place", "Echo | San Francisco (Onsite) | Sales", "Echo", "Sales", "San Francisco", "Onsite", ""},
		{"an address after a dash does not hide the employment type", "Foxtrot | Full time + Internships \u2013 https://f.example | Software Engineer", "Foxtrot", "Software Engineer", "", "", "Full time / Internships"},
		{"short role nouns name the role", "Hotel | Berlin | SWE", "Hotel", "SWE", "Berlin", "", ""},
		{"no role is titled open roles", "Acme Co | Remote (EU) | https://acme.example", "Acme Co", "Open roles", "EU", "Remote", ""},
	}
	var children []string
	for i, tc := range cases {
		children = append(children, fmt.Sprintf(`{"id":%d,"text":%q,"created_at":"2026-03-01T00:00:00Z","children":[]}`, 500+i, tc.header))
	}
	ps := hnFixture(t, map[string]string{"90": `{"id":90,"children":[` + strings.Join(children, ",") + `]}`},
		`{"hits":[{"objectID":"90","title":"Ask HN: Who is hiring? (Mar 2026)"}]}`)
	if len(ps) != len(cases) {
		t.Fatalf("got %d postings, want %d", len(ps), len(cases))
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := ps[i]
			if p.Company != tc.company || p.Title != tc.title || p.Location != tc.location ||
				p.Fields["workplace_type"] != tc.work || p.Fields["employment_type"] != tc.empl {
				t.Errorf("got company=%q title=%q location=%q work=%q employment=%q", p.Company, p.Title, p.Location, p.Fields["workplace_type"], p.Fields["employment_type"])
			}
		})
	}
}

func TestHN_lists_previous_thread(t *testing.T) {
	search := `{"hits":[
		{"objectID":"2","title":"Ask HN: Who is hiring? (Apr 2026)"},
		{"objectID":"3","title":"Ask HN: Who wants to be hired? (Mar 2026)"},
		{"objectID":"1","title":"Ask HN: Who is hiring? (Mar 2026)"},
		{"objectID":"0","title":"Ask HN: Who is hiring? (Feb 2026)"}]}`
	ps := hnFixture(t, map[string]string{
		"2": `{"id":2,"children":[{"id":601,"text":"New Co | Engineer","created_at":"2026-04-01T00:00:00Z","children":[]}]}`,
		"1": `{"id":1,"children":[{"id":602,"text":"Old Co | Engineer","created_at":"2026-03-01T00:00:00Z","children":[]}]}`,
	}, search)
	if len(ps) != 2 || !strings.Contains(ps[0].URL, "601") || !strings.Contains(ps[1].URL, "602") {
		t.Fatalf("postings = %+v, want the newest and the previous thread only", ps)
	}
}

func TestHN_spaced_engagement_tokens(t *testing.T) {
	cases := []struct {
		name  string
		token string
	}{
		{"Full time", "Full time"},
		{"Part time", "Part time"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			searchURL := "https://hn.algolia.com/api/v1/search_by_date?tags=story,author_whoishiring&hitsPerPage=10"
			storyURL := "https://hn.algolia.com/api/v1/items/80"
			searchBody := `{"hits":[{"objectID":"80","title":"Ask HN: Who is hiring? (Apr 2026)"}]}`
			storyBody := `{"id":80,"children":[
				{"id":401,"text":"Beta Inc | ` + tc.token + ` | Python developer<p>Details","created_at":"2026-04-01T00:00:00Z","children":[]}
			]}`
			m := fetchtest.NewMap()
			m.Set(searchURL, fetchtest.MapEntry{Response: fetch.Response{URL: searchURL, Status: 200, Body: []byte(searchBody)}})
			m.Set(storyURL, fetchtest.MapEntry{Response: fetch.Response{URL: storyURL, Status: 200, Body: []byte(storyBody)}})
			mod, site := source.For(mustParseURL(t, "https://news.ycombinator.com/"))
			ps, err := mod.List(context.Background(), m, site)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(ps) != 1 {
				t.Fatalf("got %d postings, want 1", len(ps))
			}
			p := ps[0]
			if p.Title != "Python developer" {
				t.Errorf("title = %q, want Python developer (%q should be engagement, not title)", p.Title, tc.token)
			}
			if p.Fields["employment_type"] == "" {
				t.Errorf("employment_type empty, want %q classified as engagement", tc.token)
			}
		})
	}
}

func TestFeed_WWR_structured_fields(t *testing.T) {
	body := mustReadFile(t, "testdata/wwr.xml")
	feedURL := "https://weworkremotely.com/remote-jobs.rss"
	m := fetchtest.NewMap()
	m.Set(feedURL, fetchtest.MapEntry{Response: fetch.Response{URL: feedURL, Status: 200, ContentType: "application/rss+xml", Body: body}})
	mod, site := source.For(mustParseURL(t, "https://weworkremotely.com/"))
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d postings, want 1", len(ps))
	}
	p := ps[0]
	if p.Location != "Worldwide" {
		t.Errorf("location = %q, want Worldwide (from wwr:country)", p.Location)
	}
	if p.Fields["employment_type"] != "Full-Time" {
		t.Errorf("employment_type = %q, want Full-Time", p.Fields["employment_type"])
	}
	if p.Expires.IsZero() {
		t.Errorf("expires not set, want date from wwr:expires_at")
	}
}

func TestFeed_Remotive_structured_fields(t *testing.T) {
	body := mustReadFile(t, "testdata/remotive.xml")
	feedURL := "https://remotive.com/api/remote-jobs"
	m := fetchtest.NewMap()
	m.Set(feedURL, fetchtest.MapEntry{Response: fetch.Response{URL: feedURL, Status: 200, ContentType: "application/rss+xml", Body: body}})
	mod, site := source.For(mustParseURL(t, feedURL))
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d postings, want 1", len(ps))
	}
	p := ps[0]
	if p.Location != "Worldwide" {
		t.Errorf("location = %q, want Worldwide (from <location>)", p.Location)
	}
	if p.Fields["employment_type"] != "full_time" {
		t.Errorf("employment_type = %q, want full_time", p.Fields["employment_type"])
	}
}

func TestJSONLD_salary_monetary_amount(t *testing.T) {
	const body = `<html><head><script type="application/ld+json">
{"@type":"JobPosting","title":"Protocol Engineer","url":"https://jobs.example.com/job/42",
"baseSalary":{"@type":"MonetaryAmount","currency":"USD","value":{"@type":"QuantitativeValue","minValue":100000,"maxValue":150000,"unitText":"YEAR"}}}
</script></head></html>`
	addr := "https://jobs.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: []byte(body)}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 1 {
		t.Fatalf("List err=%v len=%d, want 1", err, len(ps))
	}
	sal := ps[0].Fields["salary"]
	if !strings.Contains(sal, "100000") || !strings.Contains(sal, "150000") {
		t.Errorf("salary = %q, want both 100000 and 150000", sal)
	}
	if !strings.Contains(sal, "USD") {
		t.Errorf("salary = %q, want USD currency", sal)
	}
	if !strings.Contains(sal, "/year") {
		t.Errorf("salary = %q, want /year period", sal)
	}
}

func TestJSONLD_employment_type_array(t *testing.T) {
	const body = `<html><head><script type="application/ld+json">
{"@type":"JobPosting","title":"Protocol Engineer","url":"https://jobs.example.com/job/42","employmentType":["FULL_TIME","CONTRACTOR"]}
</script></head></html>`
	addr := "https://jobs.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: []byte(body)}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 1 {
		t.Fatalf("List err=%v len=%d, want 1", err, len(ps))
	}
	if ps[0].Fields["employment_type"] != "FULL_TIME" {
		t.Errorf("employment_type = %q, want FULL_TIME (first array element)", ps[0].Fields["employment_type"])
	}
}

func TestJSONLD_applicant_location_requirements(t *testing.T) {
	const body = `<html><head><script type="application/ld+json">
{"@type":"JobPosting","title":"Protocol Engineer","url":"https://jobs.example.com/job/42",
"jobLocationType":"TELECOMMUTE","applicantLocationRequirements":{"@type":"Country","name":"US"}}
</script></head></html>`
	addr := "https://jobs.example.com/"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: []byte(body)}})
	ps, err := listGeneric(t, m, addr)
	if err != nil || len(ps) != 1 {
		t.Fatalf("List err=%v len=%d, want 1", err, len(ps))
	}
	loc := ps[0].Location
	if !strings.Contains(loc, "Remote") {
		t.Errorf("location = %q, want Remote (TELECOMMUTE)", loc)
	}
	if !strings.Contains(loc, "US") {
		t.Errorf("location = %q, want US (applicantLocationRequirements)", loc)
	}
}

func TestDetail_company_recovery(t *testing.T) {
	cases := []struct {
		name, body, title, wantCo string
	}{
		{
			"json-ld no url matched by title",
			`<html><head><script type="application/ld+json">[{"@type":"JobPosting","title":"Rust Engineer","hiringOrganization":{"name":"Chain Labs"}},{"@type":"JobPosting","title":"Community Manager","hiringOrganization":{"name":"Other"}}]</script></head><body><main><p>b</p></main></body></html>`,
			"Rust Engineer", "Chain Labs",
		},
		{
			"page title fallback when no json-ld item matches",
			`<html><head><title>Rust Engineer at Chain Labs | site</title><script type="application/ld+json">[{"@type":"JobPosting","title":"Community Manager","hiringOrganization":{"name":"Chain Labs"}}]</script></head><body><p>b</p></body></html>`,
			"Rust Engineer", "Chain Labs",
		},
		{
			"ambiguous json-ld suppresses page title fallback",
			`<html><head><title>Rust Engineer at Chain Labs | site</title><script type="application/ld+json">[{"@type":"JobPosting","title":"Rust Engineer","hiringOrganization":{"name":"Chain A"}},{"@type":"JobPosting","title":"Rust Engineer","hiringOrganization":{"name":"Chain B"}}]</script></head></html>`,
			"Rust Engineer", "",
		},
		{
			"page title fills company when no json-ld present",
			`<html><head><title>Smart Contract Dev at DeFi Labs</title></head><body><main><p>desc</p></main></body></html>`,
			"Smart Contract Dev", "DeFi Labs",
		},
		{
			"json-ld title punct stripped single item fallback",
			`<html><head><script type="application/ld+json">{"@type":"JobPosting","title":"Rust Engineer, Remote","url":"https://other.example.com/job/42","hiringOrganization":{"name":"Chain Labs"},"description":"<p>text</p>"}</script></head><body><main><p>b</p></main></body></html>`,
			"Rust Engineer Remote", "Chain Labs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const pageURL = "https://jobs.example.com/vacancy/12345"
			m := fetchtest.NewMap()
			m.Set(pageURL, fetchtest.MapEntry{Response: fetch.Response{URL: pageURL, Status: 200, Body: []byte(tc.body)}})
			p := listing.Posting{URL: pageURL, Title: tc.title}
			result, err := source.Detail(context.Background(), m, p, source.Site{Address: pageURL})
			if err != nil {
				t.Fatalf("Detail: %v", err)
			}
			if result.Company != tc.wantCo {
				t.Errorf("company = %q, want %q", result.Company, tc.wantCo)
			}
		})
	}
}

func TestParadigm_js_literal_postings(t *testing.T) {
	body := mustReadFile(t, "testdata/paradigm.html")
	addr := "https://www.paradigm.xyz/careers"
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
	p0 := ps[0]
	if p0.Title != "Protocol Engineer" {
		t.Errorf("title[0] = %q", p0.Title)
	}
	if p0.Company != "Chain Labs" {
		t.Errorf("company[0] = %q", p0.Company)
	}
	if p0.Fields["salary"] != "USD 80000–120000/year" {
		t.Errorf("salary[0] = %q, want USD 80000–120000/year", p0.Fields["salary"])
	}
	if p0.Fields["workplace_type"] != "" {
		t.Errorf("workplace_type[0] = %q, want empty (remote=false)", p0.Fields["workplace_type"])
	}
	if p0.Location != "New York" {
		t.Errorf("location[0] = %q, want New York", p0.Location)
	}
	wantDate := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !p0.Published.Equal(wantDate) {
		t.Errorf("published[0] = %v, want %v", p0.Published, wantDate)
	}
	p1 := ps[1]
	if p1.Fields["workplace_type"] != "remote" {
		t.Errorf("workplace_type[1] = %q, want remote", p1.Fields["workplace_type"])
	}
	if p1.Fields["salary"] != "" {
		t.Errorf("salary[1] = %q, want empty (null salary)", p1.Fields["salary"])
	}
}

func TestParadigm_external_host_urls_listed(t *testing.T) {
	body := mustReadFile(t, "testdata/paradigm.html")
	addr := "https://www.paradigm.xyz/careers"
	m := fetchtest.NewMap()
	m.Set(addr, fetchtest.MapEntry{Response: fetch.Response{URL: addr, Status: 200, Body: body}})
	u := mustParseURL(t, addr)
	mod, site := source.For(u)
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range ps {
		if strings.Contains(p.URL, "paradigm.xyz") {
			t.Errorf("URL %q is on paradigm.xyz, want external host (record should be listed regardless)", p.URL)
		}
	}
	if len(ps) != 3 {
		t.Fatalf("got %d postings, want 3 (all records including external-host URLs)", len(ps))
	}
}

func TestParadigm_js_literal_matches_json(t *testing.T) {
	const baseURL = "https://www.paradigm.xyz/careers"
	jsonBody := `<html><head><script>var d={jobs:[{"title":"Node Operator","url":"https://careers.proto.example.com/node-op-1","companyName":"Proto Labs","companyDomain":"proto.example.com","locations":["Berlin"],"remote":false,"createdAt":"2026-06-01T00:00:00Z","salary":null,"functions":[],"skills":[],"seniorities":[],"regions":[],"isFeatured":false}],companies:[]};</script></head></html>`
	jsBody := `<html><head><script>var d={jobs:[{title:"Node Operator",url:"https://careers.proto.example.com/node-op-1",companyName:"Proto Labs",companyDomain:"proto.example.com",locations:["Berlin"],remote:false,createdAt:"2026-06-01T00:00:00Z",salary:null,functions:[],skills:[],seniorities:[],regions:[],isFeatured:false}],companies:[]};</script></head></html>`
	u := mustParseURL(t, baseURL)
	mod, site := source.For(u)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"json_keys", jsonBody},
		{"bare_keys", jsBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fetchtest.NewMap()
			m.Set(baseURL, fetchtest.MapEntry{Response: fetch.Response{URL: baseURL, Status: 200, Body: []byte(tc.body)}})
			ps, err := mod.List(context.Background(), m, site)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(ps) != 1 {
				t.Fatalf("got %d postings, want 1", len(ps))
			}
			if ps[0].Title != "Node Operator" {
				t.Errorf("title = %q", ps[0].Title)
			}
			if ps[0].Company != "Proto Labs" {
				t.Errorf("company = %q", ps[0].Company)
			}
			if ps[0].Location != "Berlin" {
				t.Errorf("location = %q, want Berlin", ps[0].Location)
			}
		})
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", s, err)
	}
	return u
}

func mustReadFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", name, err)
	}
	return b
}

const braintrustListAddress = "https://app.usebraintrust.com/api/jobs/"

func braintrustFake() *fetchtest.Map {
	m := fetchtest.NewMap()
	m.Set(braintrustListAddress, fetchtest.MapEntry{Response: fetch.Response{Status: 200, Body: []byte(`{
		"count": 2, "next": "https://app.usebraintrust.com/api/jobs/?page=2", "previous": null,
		"results": [
			{"id": 9001, "title": "Rust Developer", "employer": {"id": 1, "name": "Invented Labs"},
			 "budget_minimum_usd": "40.00", "budget_maximum_usd": "60.00", "payment_type": "hourly",
			 "job_type": "freelance", "created": "2026-03-01T10:00:00Z",
			 "locations": [{"location": "remote", "custom_location": "", "city": "Lisbon", "country": "Portugal"}]},
			{"id": 9002, "title": "Designer", "employer": {"id": 2, "name": ""}}
		]}`)}})
	m.Set("https://app.usebraintrust.com/api/jobs/9001/", fetchtest.MapEntry{Response: fetch.Response{Status: 200, Body: []byte(`{
		"id": 9001, "description": "<p>Build the invented ledger.</p>", "requirements": "<p>Five years of Rust.</p>",
		"published_at": "2026-03-02T09:00:00Z",
		"creator": {"name": "Zelda Quartz", "email": "zelda.quartz@invented.example"}}`)}})
	return m
}

func TestBraintrust_list_then_detail_gives_posting_with_text(t *testing.T) {
	m := braintrustFake()
	mod, site := source.For(mustParseURL(t, "https://www.usebraintrust.com/jobs"))
	ps, err := mod.List(context.Background(), m, site)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ps) != 1 {
		t.Fatalf("postings = %d, want 1 (the item without an employer is skipped)", len(ps))
	}
	p, err := source.Detail(context.Background(), m, ps[0], site)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if p.URL != "https://app.usebraintrust.com/jobs/9001/" {
		t.Errorf("URL = %q", p.URL)
	}
	if !strings.Contains(p.Text, "invented ledger") || !strings.Contains(p.Text, "Five years of Rust") {
		t.Errorf("Text = %q, want description and requirements", p.Text)
	}
	if got := p.Fields["salary"]; got != "USD 40–60/hour" {
		t.Errorf("salary = %q, want USD 40–60/hour", got)
	}
	all := p.Text + p.Title + p.Company + p.Location
	for _, v := range p.Fields {
		all += v
	}
	if strings.Contains(all, "Zelda") || strings.Contains(all, "zelda.quartz") {
		t.Errorf("the creator of the job leaked into the posting: %q", all)
	}
}

func TestBraintrust_reads_only_first_page(t *testing.T) {
	m := braintrustFake()
	mod, site := source.For(mustParseURL(t, "https://app.usebraintrust.com/jobs/"))
	if _, err := mod.List(context.Background(), m, site); err != nil {
		t.Fatalf("List: %v", err)
	}
	if reqs := m.Requests(); len(reqs) != 1 || reqs[0] != braintrustListAddress {
		t.Errorf("requests = %v, want only %s", reqs, braintrustListAddress)
	}
}
