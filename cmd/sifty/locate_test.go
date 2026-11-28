package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type locateFix struct {
	d    *db.DB
	base string
	web  *fetchtest.Map
}

func newLocateFix(t *testing.T) locateFix {
	t.Helper()
	d := openTestDB(t)
	web := fetchtest.NewMap()
	r := runner.New(d, nil, time.Now, slog.Default())
	registerKindsFetching(t, r, d, &models.FakeModel{}, catalog.New(d, time.Now), time.Now, web)
	base := newRouteServer(t, d, r)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Start(ctx)
	insertProfile(t, d, 1, "Backend Engineer")
	return locateFix{d: d, base: base, web: web}
}

func (f locateFix) redirect(from, to string) {
	f.web.Set(from, fetchtest.MapEntry{Response: fetch.Response{URL: to, Status: 200, ContentType: "text/html", Body: []byte("<html></html>")}})
}

func (f locateFix) board(name, company string, titles ...string) {
	jobs := make([]string, len(titles))
	for i, title := range titles {
		jobs[i] = fmt.Sprintf(`{"id":%d,"title":%q,"company_name":%q,"absolute_url":"https://boards.greenhouse.io/%s/jobs/%d"}`, i+1, title, company, name, i+1)
	}
	body := `{"jobs":[` + strings.Join(jobs, ",") + `]}`
	f.web.Set("https://boards-api.greenhouse.io/v1/boards/"+name+"/jobs", fetchtest.MapEntry{Response: fetch.Response{Status: 200, ContentType: "application/json", Body: []byte(body)}})
}

func (f locateFix) shortlist(t *testing.T, id int64) {
	t.Helper()
	_, status := apiSend(t, http.MethodPost, f.base, fmt.Sprintf("/api/vacancies/%d/decision", id), map[string]any{"profile": 1, "from": "inbox", "to": "shortlist"})
	if status != http.StatusOK {
		t.Fatalf("shortlist: status %d", status)
	}
}

func (f locateFix) searched(t *testing.T, runs int) {
	t.Helper()
	waitFor(t, 5*time.Second, func() bool {
		var n int
		f.d.QueryRow(`SELECT count(*) FROM job WHERE kind='locate' AND state='done'`).Scan(&n)
		return n >= runs
	})
}

func (f locateFix) apply(t *testing.T, id int64) map[string]any {
	t.Helper()
	view, status := apiGet(t, f.base, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
	if status != http.StatusOK {
		t.Fatalf("view: status %d", status)
	}
	actions, _ := view["actions"].([]any)
	for _, a := range actions {
		if m, _ := a.(map[string]any); m["key"] == "apply" {
			return m
		}
	}
	t.Fatalf("no apply action in %v", actions)
	return nil
}

func TestFoundEmployerPageIsNotReplacedByLaterLocate(t *testing.T) {
	f := newLocateFix(t)
	listingURL := "https://jobs.aggregator.example.test/view/77"
	id := insertFullVacancy(t, f.d, listingURL, "Backend Engineer", "Acme Robotics")
	first := "https://boards.greenhouse.io/acme-robotics/jobs/4001"
	f.redirect(listingURL, first+"?gh_src=tracker")

	f.shortlist(t, id)
	f.searched(t, 1)
	if a := f.apply(t, id); a["via"] != "via employer site" || a["url"] != first {
		t.Fatalf("after Shortlist: apply = %v, want via employer site at %s", a, first)
	}

	f.redirect(listingURL, "https://boards.greenhouse.io/acme-robotics/jobs/9999")
	if _, status := apiSend(t, http.MethodPost, f.base, fmt.Sprintf("/api/vacancies/%d/locate", id), map[string]any{"profile": 1}); status != http.StatusOK {
		t.Fatalf("locate: status %d", status)
	}
	f.searched(t, 2)
	if a := f.apply(t, id); a["url"] != first {
		t.Fatalf("after a later locate: apply url = %v, want %s", a["url"], first)
	}
}

func TestBoardJobOfAnotherTitleOrAnotherCompanyIsNotAnEmployerPage(t *testing.T) {
	cases := []struct {
		name, board, company, vacancy string
		titles                        []string
		found                         string
	}{
		{"another title on the company's board", "acme-robotics", "Acme Robotics", "Backend Engineer", []string{"Frontend Engineer"}, ""},
		{"same title on the board of another company", "acme", "Acme", "Backend Engineer", []string{"Backend Engineer"}, ""},
		{"two jobs that extend the title in different ways", "acme-robotics", "Acme Robotics", "Backend Engineer", []string{"Backend Engineer - Payments", "Backend Engineer - Ledger"}, ""},
		{"one job that extends the title with a suffix", "acme-robotics", "Acme Robotics", "IT Department Lead", []string{"Office Manager", "IT Department Lead - Remote, Worldwide"}, "https://boards.greenhouse.io/acme-robotics/jobs/2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocateFix(t)
			listingURL := "https://jobs.aggregator.example.test/view/1"
			id := insertFullVacancy(t, f.d, listingURL, tc.vacancy, "Acme Robotics")
			f.board(tc.board, tc.company, tc.titles...)

			f.shortlist(t, id)
			f.searched(t, 1)
			a := f.apply(t, id)
			if tc.found != "" {
				if a["via"] != "via employer site" || a["url"] != tc.found {
					t.Fatalf("apply = %v, want via employer site at %s", a, tc.found)
				}
				return
			}
			if a["via"] != "search for employer site" || a["search"] != true || a["url"] != listingURL {
				t.Fatalf("apply = %v, want the listing offered with a new search", a)
			}
		})
	}
}
