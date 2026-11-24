package inbox_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
)

func addAtLocation(t *testing.T, cat *catalog.Catalog, url, title, location string) int64 {
	t.Helper()
	p := postWithText(url, title, "Co", "job")
	p.Location = location
	return add(t, cat, p)
}

func publishedFixture(t *testing.T) *db.DB {
	t.Helper()
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"engineer"}, nil)
	cat := newCat(d)
	for _, tc := range []struct {
		name string
		ago  int
	}{{"D6", 6}, {"D8", 8}, {"D29", 29}, {"D31", 31}, {"Undated", 0}} {
		p := post("https://example.com/jobs/pub-"+tc.name, tc.name+" Engineer", "Co")
		if tc.ago != 0 {
			p.Published = testNow.AddDate(0, 0, -tc.ago)
			p.PublishedBasis = "datePosted"
		}
		add(t, cat, p)
	}
	return d
}

func TestFilter_PublishedKeepsRecentAndUndatedVacancies(t *testing.T) {
	ts := newTestServer(t, publishedFixture(t))
	for filter, want := range map[string]string{
		"":   "D29 Engineer,D31 Engineer,D6 Engineer,D8 Engineer,Undated Engineer",
		"7":  "D6 Engineer,Undated Engineer",
		"30": "D29 Engineer,D6 Engineer,D8 Engineer,Undated Engineer",
	} {
		if got := sortedTitles(apiGet(t, ts, "/api/vacancies?published="+filter)); got != want {
			t.Errorf("published=%q: rows %q, want %q", filter, got, want)
		}
	}
}

func TestExpiry_StageCondition(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"engineer"}, nil)
	cat := newCat(d)
	expire := func(url, title string, days int) int64 {
		p := post(url, title, "Co")
		p.Expires = testNow.AddDate(0, 0, days)
		return add(t, cat, p)
	}
	expSL := expire("https://example.com/jobs/ec1", "Expired Engineer", -1)
	setVacancyIndustries(t, d, expSL, []string{"Ops"})
	decide(t, d, expSL, "shortlist", "")
	expAP := expire("https://example.com/jobs/ec2", "Applied Expired Engineer", -2)
	setVacancyIndustries(t, d, expAP, []string{"Ops"})
	decide(t, d, expAP, "applied", "")
	expire("https://example.com/jobs/ec3", "Future Engineer", 10)
	openDM := add(t, cat, post("https://example.com/jobs/ec4", "Live Engineer", "Co"))
	decide(t, d, openDM, "dismissed", "")

	ts := newTestServer(t, d)
	ib := apiGet(t, ts, "/api/vacancies?stage=inbox")
	if n, s, ap, dm := countOf(ib, "inbox"), countOf(ib, "shortlist"), countOf(ib, "applied"), countOf(ib, "dismissed"); n != 1 || s != 0 || ap != 1 || dm != 1 {
		t.Errorf("counts inbox/shortlist/applied/dismissed: %.0f/%.0f/%.0f/%.0f, want 1/0/1/1", n, s, ap, dm)
	}
	sl := apiGet(t, ts, "/api/vacancies?stage=shortlist")
	ap := apiGet(t, ts, "/api/vacancies?stage=applied")
	apRS := getRows(ap)
	if len(apRS) != 1 || rowID(apRS[0]) != float64(expAP) {
		t.Errorf("applied rows: %d, want 1", len(apRS))
	}
	if exp, _ := apRS[0].(map[string]any)["expired"].(bool); !exp {
		t.Errorf("applied row: expired should be true")
	}
	if ic := namedCounts(sl, "industryCounts")["Ops"]; ic != 0 {
		t.Errorf("shortlist Ops industry count: %d, want 0", ic)
	}
	if ic := namedCounts(ap, "industryCounts")["Ops"]; ic != 1 {
		t.Errorf("applied Ops industry count: %d, want 1", ic)
	}
}

func TestFilter_Remote(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"engineer"}, nil)
	cat := newCat(d)
	for i, tc := range []struct{ title, location, industry string }{
		{"Remote EU Engineer", "Remote · EU", "Software"},
		{"Hybrid Engineer", "Remote-first (hybrid)", ""},
		{"Office Engineer", "New York office only", "Software"},
		{"Anywhere Engineer", "Worldwide – anywhere", ""},
		{"Telecommute Engineer", "Telecommute · US", ""},
		{"City Engineer", "San Francisco", ""},
		{"Many Places Engineer", "Berlin · Paris · Madrid · Rome · Remote", ""},
	} {
		id := addAtLocation(t, cat, fmt.Sprintf("https://example.com/jobs/rm%d", i), tc.title, tc.location)
		if tc.industry != "" {
			setVacancyIndustries(t, d, id, []string{tc.industry})
		}
	}
	body := apiGet(t, newTestServer(t, d), "/api/vacancies?remote=true")
	want := "Anywhere Engineer,Hybrid Engineer,Many Places Engineer,Remote EU Engineer,Telecommute Engineer"
	if got := sortedTitles(body); got != want {
		t.Errorf("remote rows: got %q, want %q", got, want)
	}
	if n := namedCounts(body, "industryCounts")["Software"]; n != 1 {
		t.Errorf("industryCounts with remote=true: Software=%d, want 1 (office vacancy excluded)", n)
	}
}

func TestFilter_Leadership(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"engineer", "developer", "head", "director", "manager", "lead", "cto", "analyst"}, nil)
	cat := newCat(d)

	cases := []struct {
		title string
		match bool
	}{
		{"Head of Engineering", true},
		{"Lead Generation Specialist", false},
		{"CTO (remote)", true},
		{"Senior Director of Product", true},
		{"Engineering Manager", true},
		{"Lead Software Engineer", true},
		{"Data Analyst", false},
		{"Head of Platform Engineering", true},
		{"Lead Generation Director", true},
	}
	for i, tc := range cases {
		add(t, cat, post(fmt.Sprintf("https://example.com/jobs/l%d", i), tc.title, "Co"))
	}

	matched := map[string]bool{}
	for _, r := range getRows(apiGet(t, newTestServer(t, d), "/api/vacancies?leadership=true")) {
		matched[field(r, "title")] = true
	}
	for _, tc := range cases {
		if matched[tc.title] != tc.match {
			t.Errorf("leadership title=%q: matched=%v, want %v", tc.title, matched[tc.title], tc.match)
		}
	}
}

func sourceFixture(t *testing.T) (*db.DB, *httptest.Server) {
	t.Helper()
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"engineer"}, nil)
	cat := newCat(d)
	idA := addWithSrc(t, cat, post("https://example.com/jobs/s1", "Alpha Engineer", "Co"), "feed-alpha")
	idB := addWithSrc(t, cat, post("https://example.com/jobs/s2", "Beta Engineer A", "Co"), "feed-beta")
	addWithSrc(t, cat, post("https://example.com/jobs/s3", "Beta Engineer B", "Co"), "feed-beta")
	addWithSrc(t, cat, post("https://example.com/jobs/s4", "Gamma Engineer", "Co"), "feed-gamma")
	setVacancyIndustries(t, d, idA, []string{"Web3"})
	setVacancyIndustries(t, d, idB, []string{"Software"})
	return d, newTestServer(t, d)
}

func TestFilter_Sources(t *testing.T) {
	_, ts := sourceFixture(t)
	for _, tc := range []struct{ name, query, want string }{
		{"one source keeps only its roles", "?sources=feed-alpha", "Alpha Engineer"},
		{"several sources keep the roles of each", "?sources=feed-alpha,feed-beta",
			"Alpha Engineer,Beta Engineer A,Beta Engineer B"},
		{"no source filter keeps every role", "",
			"Alpha Engineer,Beta Engineer A,Beta Engineer B,Gamma Engineer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sortedTitles(apiGet(t, ts, "/api/vacancies"+tc.query)); got != tc.want {
				t.Errorf("rows: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFilter_SourceAndIndustryCounts(t *testing.T) {
	_, ts := sourceFixture(t)
	want := map[string]int{"feed-alpha": 1, "feed-beta": 2, "feed-gamma": 1}
	for _, query := range []string{"", "?sources=feed-alpha"} {
		got := namedCounts(apiGet(t, ts, "/api/vacancies"+query), "sourceCounts")
		for name, n := range want {
			if got[name] != n {
				t.Errorf("sourceCounts%q: %s=%d, want %d (the sources filter is ignored)", query, name, got[name], n)
			}
		}
	}
	ic := namedCounts(apiGet(t, ts, "/api/vacancies?sources=feed-alpha"), "industryCounts")
	if ic["Web3"] != 1 || ic["Software"] != 0 {
		t.Errorf("industryCounts with the sources filter: Web3=%d Software=%d, want 1 and 0", ic["Web3"], ic["Software"])
	}
}

func TestFilter_Unread(t *testing.T) {
	for _, stage := range []string{"applied", "dismissed"} {
		t.Run("ignored in "+stage, func(t *testing.T) {
			d := openTestDB(t)
			setProfileData(t, d, nil, []string{"engineer"}, nil)
			cat := newCat(d)
			idUnread := add(t, cat, post("https://example.com/jobs/u1", "Unread Engineer", "Co"))
			idRead := add(t, cat, post("https://example.com/jobs/u2", "Read Engineer", "Co"))
			d.Exec(`UPDATE vacancy SET seen_at=? WHERE id=?`, testNow.UTC().Format(time.RFC3339), idRead)

			ts := newTestServer(t, d)
			rs := getRows(apiGet(t, ts, "/api/vacancies?unread=true"))
			if len(rs) != 1 || rowID(rs[0]) != float64(idUnread) {
				t.Errorf("unread in inbox: got %d rows, want 1 (unread)", len(rs))
			}
			decide(t, d, idRead, stage, "")
			decide(t, d, idUnread, stage, "")
			if rs := getRows(apiGet(t, ts, "/api/vacancies?stage="+stage+"&unread=true")); len(rs) != 2 {
				t.Errorf("unread ignored in %s: got %d rows, want 2", stage, len(rs))
			}
		})
	}
}

func TestFilter_Combined(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"engineer"}, nil)
	cat := newCat(d)
	at := func(url, location string, ago int) int64 {
		p := postWithText(url, "Engineer", "Co", "job")
		p.Location = location
		p.Published = testNow.AddDate(0, 0, -ago)
		p.PublishedBasis = "datePosted"
		return add(t, cat, p)
	}
	idOK := at("https://example.com/jobs/cb1", "Remote · EU", 3)
	at("https://example.com/jobs/cb2", "London", 3)
	at("https://example.com/jobs/cb3", "Remote · US", 15)

	rs := getRows(apiGet(t, newTestServer(t, d), "/api/vacancies?remote=true&published=7"))
	if len(rs) != 1 || rowID(rs[0]) != float64(idOK) {
		t.Errorf("combined remote+published: got %d rows, want 1", len(rs))
	}
}

func TestFilter_Total_UnchangedByFilters(t *testing.T) {
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"engineer"}, nil)
	cat := newCat(d)
	for i := 0; i < 3; i++ {
		addAtLocation(t, cat, fmt.Sprintf("https://example.com/jobs/tot%d", i), "Engineer", "London")
	}

	ts := newTestServer(t, d)
	for _, query := range []string{"", "?remote=true", "?q=engineer"} {
		body := apiGet(t, ts, "/api/vacancies"+query)
		if total, _ := body["total"].(float64); total != 3 {
			t.Errorf("total for %q: got %v, want 3", query, total)
		}
		if query == "?remote=true" && len(getRows(body)) != 0 {
			t.Errorf("rows with remote filter: want 0")
		}
	}
}

func TestFilter_HTTPStatus_InvalidStageWithFilters(t *testing.T) {
	d := openTestDB(t)
	ts := newTestServer(t, d)
	if _, status := apiGetRaw(t, ts, "/api/vacancies?stage=bogus&remote=true"); status != http.StatusUnprocessableEntity {
		t.Errorf("invalid stage with filters: status %d, want 422", status)
	}
}
