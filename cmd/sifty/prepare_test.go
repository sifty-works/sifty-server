package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type formSite struct {
	mu     sync.Mutex
	prefix string
	body   string
	status int
}

func (s *formSite) answer(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body = status, body
}

func (s *formSite) Get(_ context.Context, url string, _ fetch.Opt) (fetch.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.HasPrefix(url, s.prefix) {
		return fetch.Response{}, fetch.ErrNotFound
	}
	return fetch.Response{URL: url, Status: s.status, Body: []byte(s.body)}, nil
}

type formFix struct {
	d       *db.DB
	base    string
	site    *formSite
	vacancy int64
}

type noDrafts struct{}

func (noDrafts) Complete(context.Context, models.Request) (models.Response, error) {
	return models.Response{Text: `{"answers":[]}`}, nil
}

func newFormFix(t *testing.T, pageURL, dataPrefix string) formFix {
	t.Helper()
	return newFormFixWith(t, pageURL, dataPrefix, noDrafts{})
}

func newFormFixWith(t *testing.T, pageURL, dataPrefix string, m models.Model) formFix {
	t.Helper()
	return newFormFixOn(t, pageURL, dataPrefix, func(*db.DB, *config.Store) modelSource { return fixedModel{m} }, filepath.Join(t.TempDir(), "sifty.yaml"))
}

func newFormFixOn(t *testing.T, pageURL, dataPrefix string, source func(*db.DB, *config.Store) modelSource, settings string) formFix {
	t.Helper()
	d := openTestDB(t)
	r := runner.New(d, nil, time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	site := &formSite{prefix: dataPrefix, status: 200}
	src := registerKindsOn(t, r, d, source, settings, catalog.New(d, time.Now), time.Now, site)
	base := newRouteServerOn(t, d, r, candidate.Readers{PDF: candidate.ProgramText{Program: filepath.Join(t.TempDir(), "absent")}}, nil, src)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Start(ctx)
	insertProfile(t, d, 1, "Backend Engineer")
	return formFix{d: d, base: base, site: site, vacancy: insertFullVacancy(t, d, pageURL, "Backend Engineer", "Stackworks")}
}

func (f formFix) shortlist(t *testing.T) {
	t.Helper()
	out, status := apiSend(t, http.MethodPost, f.base, fmt.Sprintf("/api/vacancies/%d/decision", f.vacancy), map[string]any{"profile": 1, "from": "inbox", "to": "shortlist"})
	if status != http.StatusOK {
		t.Fatalf("shortlist: status %d: %v", status, out)
	}
}

func (f formFix) application(t *testing.T) map[string]any {
	t.Helper()
	out, status := apiGet(t, f.base, fmt.Sprintf("/api/vacancies/%d/application", f.vacancy))
	if status != http.StatusOK {
		t.Fatalf("GET application: status %d: %v", status, out)
	}
	return out
}

func (f formFix) settled(t *testing.T) map[string]any {
	t.Helper()
	var view map[string]any
	waitFor(t, 5*time.Second, func() bool {
		view = f.application(t)
		return view["state"] != "reading" && view["state"] != "unread"
	})
	return view
}

func fieldLines(view map[string]any) []string {
	var out []string
	fields, _ := view["fields"].([]any)
	for _, raw := range fields {
		f := raw.(map[string]any)
		var options []string
		for _, o := range f["options"].([]any) {
			options = append(options, o.(string))
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|%v|%s", f["key"], f["label"], f["kind"], f["required"], strings.Join(options, ",")))
	}
	return out
}

const ashbyPage = "https://jobs.ashbyhq.com/stackworks/0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d"

const ashbyForm = `{"data":{"jobPosting":{"applicationForm":{"sections":[{"fieldEntries":[
{"id":"e1","field":{"path":"_systemfield_name","title":"Name","type":"String"},"isRequired":true,"isHidden":null},
{"id":"e2","field":{"path":"team","title":"Team preference","type":"ValueSelect","selectableValues":[{"label":"Platform","value":"Platform"},{"label":"Product","value":"Product"}]},"isRequired":false,"isHidden":null},
{"id":"e3","field":{"path":"story","title":"Tell us about a hard project","type":"LongText"},"isRequired":true,"isHidden":null},
{"id":"e4","field":{"path":"referrer","title":"Referral code","type":"String"},"isRequired":false,"isHidden":true}
]}]}}}}`

func TestShortlistedAshbyVacancyGetsItsFormReadWithRequiredMarksAndOptions(t *testing.T) {
	f := newFormFix(t, ashbyPage, "https://jobs.ashbyhq.com/api/non-user-graphql")
	f.site.answer(200, ashbyForm)
	f.shortlist(t)

	view := f.settled(t)
	if view["state"] != "ready" {
		t.Fatalf("state = %v, view %v", view["state"], view)
	}
	want := []string{
		"team|Team preference|choice|false|Platform,Product",
		"story|Tell us about a hard project|long_text|true|",
	}
	if got := fieldLines(view); !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %q\nwant     %q", got, want)
	}
}

func TestKnownSystemWithoutQuestionListIsUnreadableNotAnEmptyForm(t *testing.T) {
	f := newFormFix(t, "https://job-boards.greenhouse.io/stackworks/jobs/4001", "https://boards-api.greenhouse.io/v1/boards/stackworks/jobs/4001?")
	f.site.answer(200, `{"id":4001,"title":"Backend Engineer"}`)
	f.shortlist(t)

	view := f.settled(t)
	if view["state"] != "unreadable" || view["text"] != "The Greenhouse form could not be read" || view["action"] != "Paste the form" {
		t.Errorf("view = %v", view)
	}
	if got := fieldLines(view); len(got) != 0 {
		t.Errorf("fields = %q, want none", got)
	}
}

const greenhouseFirst = `{"questions":[
{"label":"Why Stackworks?","required":false,"fields":[{"name":"question_1","type":"textarea","values":[]}]},
{"label":"Notice period","required":true,"fields":[{"name":"question_2","type":"input_text","values":[]}]}
]}`

const greenhouseSecond = `{"questions":[
{"label":"Which team fits you best?","required":true,"fields":[{"name":"question_2","type":"multi_value_single_select","values":[{"label":"Below 50k","value":1},{"label":"Above 50k","value":2}]}]},
{"label":"Why Stackworks?","required":false,"fields":[{"name":"question_9","type":"textarea","values":[]}]}
]}`

func TestReadingAgainKeepsAFieldByItsUniqueLabelAndDropsOneThatVanished(t *testing.T) {
	f := newFormFix(t, "https://job-boards.greenhouse.io/stackworks/jobs/4001", "https://boards-api.greenhouse.io/v1/boards/stackworks/jobs/4001?")
	f.site.answer(200, greenhouseFirst)
	f.shortlist(t)
	f.settled(t)

	f.site.answer(200, greenhouseSecond)
	out, status := apiSend(t, http.MethodPost, f.base, fmt.Sprintf("/api/vacancies/%d/application/retry", f.vacancy), nil)
	if status != http.StatusOK {
		t.Fatalf("retry: status %d: %v", status, out)
	}
	view := f.settled(t)
	want := []string{
		"question_2|Which team fits you best?|choice|true|Below 50k,Above 50k",
		"question_1|Why Stackworks?|long_text|false|",
	}
	if got := fieldLines(view); !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %q\nwant     %q", got, want)
	}
}

func TestFormTemplateTakingItsHostFromThePageAddressIsRefusedAndBuiltInOnesUsed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vocab"), 0700); err != nil {
		t.Fatal(err)
	}
	forms := "forms:\n  - hosts: [careers.example.test]\n    system: Example\n    page: '^/(?P<sub>[a-z]+)/(?P<id>[0-9]+)'\n    data: https://{sub}.example.test/api/{id}\n    questions:\n      - list: questions\n        label: label\n"
	if err := os.WriteFile(filepath.Join(dir, "vocab", "forms.yaml"), []byte(forms), 0600); err != nil {
		t.Fatal(err)
	}
	got := recoveredAtStart(t, dir)
	want := "Your form templates file vocab/forms.yaml could not be used and was moved to " + brokenFileIn(t, filepath.Join(dir, "vocab"), "forms.yaml") + ". " +
		"The host careers.example.test takes the host of its data address from the page's address; a template fixes the host. The built-in form templates are used."
	if len(got) != 1 || got[0].File != "forms.yaml" || got[0].Text != want {
		t.Errorf("recovered = %+v, want text %q", got, want)
	}
}
