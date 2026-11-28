package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
)

type drafting struct {
	mu      sync.Mutex
	prompts []string
	team    string
	empty   bool
}

func (m *drafting) Complete(_ context.Context, r models.Request) (models.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !strings.Contains(r.Prompt, "\nQUESTIONS\n") {
		return models.Response{Text: `{"answers":[]}`}, nil
	}
	m.prompts = append(m.prompts, r.Prompt)
	story := `"I built payment systems."`
	if m.empty && len(m.prompts) <= 5 {
		story = ""
	}
	return models.Response{Text: fmt.Sprintf(`{"answers":[
{"key":"story","skip":false,"values":[`+story+`],"evidence":"Payment systems engineer.","note":"","use":""},
{"key":"team","skip":false,"values":[%q],"evidence":"","note":"","use":""},
{"key":"mailbox","skip":false,"values":["wrong@example.test"],"evidence":"","note":"","use":"email"}]}`, m.team)}, nil
}

func (m *drafting) asked() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.prompts...)
}

func (f formFix) send(t *testing.T, method, path string, payload any) map[string]any {
	t.Helper()
	out, status := apiSend(t, method, f.base, path, payload)
	if status != http.StatusOK {
		t.Fatalf("%s %s: status %d: %v", method, path, status, out)
	}
	return out
}

func (f formFix) field(t *testing.T, key string) map[string]any {
	t.Helper()
	fields, _ := f.application(t)["fields"].([]any)
	for _, raw := range fields {
		if fl := raw.(map[string]any); fl["key"] == key {
			return fl
		}
	}
	return nil
}

func (f formFix) retry(t *testing.T) map[string]any {
	t.Helper()
	f.send(t, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/application/retry", f.vacancy), nil)
	return f.settled(t)
}

const mailboxEntry = `,
{"id":"e5","field":{"path":"mailbox","title":"Where can we write to you?","type":"String"},"isRequired":false,"isHidden":null}
]}]}}}}`

func draftingFix(t *testing.T, team string) (formFix, *drafting) {
	t.Helper()
	m := &drafting{team: team}
	f := newFormFixWith(t, ashbyPage, "https://jobs.ashbyhq.com/api/non-user-graphql", m)
	f.site.answer(200, strings.TrimSuffix(ashbyForm, "\n]}]}}}}")+mailboxEntry)
	f.send(t, http.MethodPatch, "/api/me", map[string]any{"email": "rowan@example.test"})
	return f, m
}

func TestDraftingPromptHoldsEvidenceAnswersAndVacancyButNoContacts(t *testing.T) {
	f, m := draftingFix(t, "Platform")
	f.send(t, http.MethodPatch, "/api/me", map[string]any{
		"legalName": "Rowan Quill", "email": "rowan@example.test", "phone": "+000 000 0000",
		"linkedin": "https://linkedin.example.test/in/rowan",
	})
	doc := f.send(t, http.MethodPost, "/api/documents", map[string]any{"name": "cv.txt", "text": "Rowan Quill\nrowan@example.test | +000 000 0000\nPayment systems engineer with unique-evidence-marker."})
	f.send(t, http.MethodPatch, fmt.Sprintf("/api/documents/%.0f", doc["id"]), map[string]any{"evidence": true})
	f.send(t, http.MethodPost, "/api/qa", map[string]any{"question": "Why do you like hard problems?", "answer": "Because of unique-qa-marker, reach me at rowan@example.test."})
	f.shortlist(t)
	f.settled(t)

	prompts := m.asked()
	if len(prompts) != 1 {
		t.Fatalf("drafting calls = %d, want 1", len(prompts))
	}
	for _, want := range []string{"unique-evidence-marker", "unique-qa-marker", "distributed systems", "Backend Engineer", "Stackworks", "Tell us about a hard project"} {
		if !strings.Contains(prompts[0], want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	stored, _ := apiGet(t, f.base, fmt.Sprintf("/api/extension/fill/%d", f.vacancy))
	var mailbox map[string]any
	for _, raw := range stored["answers"].([]any) {
		if a := raw.(map[string]any); a["key"] == "mailbox" {
			mailbox = a
		}
	}
	if mailbox["value"] != "rowan@example.test" || mailbox["source"] != "my_data" || f.field(t, "mailbox") != nil {
		t.Errorf("a field the model named as the email = %v (listed: %v), want the My data value, not listed", mailbox, f.field(t, "mailbox"))
	}
	for _, banned := range []string{"rowan@example.test", "+000 000 0000", "Rowan Quill", "linkedin.example.test"} {
		if strings.Contains(prompts[0], banned) {
			t.Errorf("prompt holds %q", banned)
		}
	}
}

func TestDraftedChoiceOutsideTheOptionsIsClearedAndOnlyOpenQuestionsAreListed(t *testing.T) {
	f, _ := draftingFix(t, "Marketing")
	f.shortlist(t)
	view := f.settled(t)

	want := []string{
		"team|Team preference|choice|false|Platform,Product",
		"story|Tell us about a hard project|long_text|true|",
	}
	if got := fieldLines(view); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("listed fields = %q, want only the open questions %q", got, want)
	}
	team := f.field(t, "team")
	if vals, _ := team["values"].([]any); len(vals) != 0 || team["source"] != "" || team["note"] == "" {
		t.Errorf("a drafted choice outside the options was kept: %v", team)
	}
	story := f.field(t, "story")
	if vals, _ := story["values"].([]any); len(vals) != 1 || vals[0] != "I built payment systems." || story["source"] != "drafted" || story["evidence"] != "Payment systems engineer." {
		t.Errorf("story = %v", story)
	}
}

func TestSecondPreparationDoesNotDraftAgainAndKeepsTheUsersEdit(t *testing.T) {
	f, m := draftingFix(t, "Platform")
	f.shortlist(t)
	f.settled(t)
	story := f.field(t, "story")
	f.send(t, http.MethodPatch, fmt.Sprintf("/api/vacancies/%d/application/fields/story", f.vacancy), map[string]any{"values": []string{"My own words."}, "revision": story["revision"]})

	f.retry(t)

	if n := len(m.asked()); n != 1 {
		t.Errorf("drafting calls = %d after a second preparation, want 1", n)
	}
	if vals, _ := f.field(t, "story")["values"].([]any); len(vals) != 1 || vals[0] != "My own words." {
		t.Errorf("story = %v, want the user's edit", vals)
	}
	if vals, _ := f.field(t, "team")["values"].([]any); len(vals) != 1 || vals[0] != "Platform" {
		t.Errorf("team = %v, want the drafted choice kept", vals)
	}
}

const residenceForm = `{"data":{"jobPosting":{"applicationForm":{"sections":[{"fieldEntries":[
{"id":"e1","field":{"path":"sponsor","title":"Will you require employer sponsorship to work in the country where you currently reside?","type":"Boolean"},"isRequired":true,"isHidden":null}
]}]}}}}`

func TestSponsorshipQuestionAboutTheCountryOfResidenceUsesTheLocationInMyData(t *testing.T) {
	for _, c := range []struct {
		residence string
		want      string
	}{{"Germany", "No"}, {"Japan", ""}, {"Canada", ""}} {
		t.Run(c.residence, func(t *testing.T) {
			f := newFormFixWith(t, ashbyPage, "https://jobs.ashbyhq.com/api/non-user-graphql", noDrafts{})
			f.site.answer(200, residenceForm)
			f.send(t, http.MethodPatch, "/api/me", map[string]any{
				"currentLocation":    map[string]any{"country": c.residence},
				"workAuthorizations": []map[string]any{{"country": "DE", "status": "eligible"}, {"country": "CA", "status": "need_visa"}},
			})
			f.shortlist(t)
			f.settled(t)
			var vals []any
			stored, _ := apiGet(t, f.base, fmt.Sprintf("/api/extension/fill/%d", f.vacancy))
			for _, raw := range stored["answers"].([]any) {
				if a := raw.(map[string]any); a["key"] == "sponsor" {
					vals = a["values"].([]any)
				}
			}
			if c.want == "No" && (len(vals) != 1 || vals[0] != "No") {
				t.Errorf("living in %s: answer = %v, want No", c.residence, vals)
			}
			if c.want == "" && len(vals) == 1 && vals[0] == "No" {
				t.Errorf("living in %s with no authorization there: answered No", c.residence)
			}
		})
	}
}

func TestPreparationOnPaidProviderIsRefusedBeforeAnyRequestWhileBulkWorkIsNotAllowed(t *testing.T) {
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(provider.Close)
	settings := filepath.Join(t.TempDir(), "sifty.yaml")
	cfg := fmt.Sprintf("providers:\n  paidbox: {type: openai, model: gpt-turbo, cost: paid, endpoint: %q}\nwork: {reading: paidbox, shortlist_check: paidbox, preparation: paidbox}\n", provider.URL)
	if err := os.WriteFile(settings, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	f := newFormFixOn(t, ashbyPage, "https://jobs.ashbyhq.com/api/non-user-graphql", func(d *db.DB, store *config.Store) modelSource {
		return models.NewGate(d, store, false, nil, nil)
	}, settings)
	f.site.answer(200, ashbyForm)
	f.shortlist(t)
	view := f.settled(t)

	if n := requests.Load(); n != 0 {
		t.Errorf("paid provider received %d requests, want 0", n)
	}
	if view["state"] != "failed" || view["text"] == "" {
		t.Errorf("view = %v, want a failed preparation that says why", view)
	}
}

func TestOpenQuestionLeftEmptyIsAskedAgainExactlyOnce(t *testing.T) {
	f, m := draftingFix(t, "Platform")
	m.empty = true
	f.shortlist(t)
	f.settled(t)

	prompts := m.asked()
	if len(prompts) != 2 {
		t.Fatalf("drafting calls = %d, want 2", len(prompts))
	}
	if second := prompts[1][strings.Index(prompts[1], "\nQUESTIONS\n"):]; !strings.Contains(second, "Tell us about a hard project") || strings.Contains(second, "Team preference") {
		t.Errorf("the second call must ask only the question left empty: %q", second)
	}
	if vals, _ := f.field(t, "story")["values"].([]any); len(vals) != 0 {
		t.Errorf("story = %v, want no answer after two empty replies", vals)
	}
}
