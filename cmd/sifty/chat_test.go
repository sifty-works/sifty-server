package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/sifty-works/sifty-server/internal/models"
)

type chatting struct {
	mu      sync.Mutex
	prompts []string
	reply   string
}

func (m *chatting) Complete(_ context.Context, r models.Request) (models.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !strings.Contains(r.Prompt, "\nQUESTIONS OF THE FORM\n") {
		return models.Response{Text: `{"answers":[]}`}, nil
	}
	m.prompts = append(m.prompts, r.Prompt)
	return models.Response{Text: m.reply}, nil
}

func (m *chatting) asked() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.prompts...)
}

func chatFix(t *testing.T) (formFix, *chatting) {
	t.Helper()
	m := &chatting{reply: `{"comment":"Done.","field":"team","values":["Product"]}`}
	f := newFormFixWith(t, ashbyPage, "https://jobs.ashbyhq.com/api/non-user-graphql", m)
	f.site.answer(200, ashbyForm)
	return f, m
}

func (f formFix) chat(t *testing.T, vacancy int64, message, field string, values []string) []any {
	t.Helper()
	out := f.send(t, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/application/chat", vacancy), map[string]any{"message": message, "field": field, "values": values})
	messages, _ := out["messages"].([]any)
	return messages
}

func (f formFix) saveAnswer(t *testing.T, vacancy int64, key string, values []string) {
	t.Helper()
	view, _ := apiGet(t, f.base, fmt.Sprintf("/api/vacancies/%d/application", vacancy))
	var revision any
	for _, raw := range view["fields"].([]any) {
		if fl := raw.(map[string]any); fl["key"] == key {
			revision = fl["revision"]
		}
	}
	f.send(t, http.MethodPatch, fmt.Sprintf("/api/vacancies/%d/application/fields/%s", vacancy, key), map[string]any{"values": values, "revision": revision})
}

func (f formFix) qaItems(t *testing.T) []any {
	t.Helper()
	out, _ := apiGet(t, f.base, "/api/qa")
	items, _ := out["items"].([]any)
	return items
}

func (f formFix) another(t *testing.T, page, company string) int64 {
	t.Helper()
	id := insertFullVacancy(t, f.d, page, "Backend Engineer", company)
	f.send(t, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/decision", id), map[string]any{"profile": 1, "from": "inbox", "to": "shortlist"})
	waitFor(t, 5e9, func() bool {
		view, _ := apiGet(t, f.base, fmt.Sprintf("/api/vacancies/%d/application", id))
		return view["state"] == "ready"
	})
	return id
}

func (f formFix) storyOf(t *testing.T, vacancy int64) map[string]any {
	t.Helper()
	view, _ := apiGet(t, f.base, fmt.Sprintf("/api/vacancies/%d/application", vacancy))
	for _, raw := range view["fields"].([]any) {
		if fl := raw.(map[string]any); fl["key"] == "story" {
			return fl
		}
	}
	return nil
}

func TestChatMessageStoresBothSidesAndSendsOneCallWithScrubbedContextAndTheLastTenMessages(t *testing.T) {
	f, m := chatFix(t)
	f.send(t, http.MethodPatch, "/api/me", map[string]any{
		"legalName": "Rowan Quill", "email": "rowan@example.test", "phone": "+000 000 0000",
		"linkedin": "https://linkedin.example.test/in/rowan",
	})
	doc := f.send(t, http.MethodPost, "/api/documents", map[string]any{"name": "cv.txt", "text": "Rowan Quill\nrowan@example.test | +000 000 0000\nPayment systems engineer with unique-evidence-marker."})
	f.send(t, http.MethodPatch, fmt.Sprintf("/api/documents/%.0f", doc["id"]), map[string]any{"evidence": true})
	f.send(t, http.MethodPost, "/api/qa", map[string]any{"question": "Why do you like hard problems?", "answer": "Because of unique-qa-marker, reach me at rowan@example.test."})
	f.shortlist(t)
	f.settled(t)
	f.saveAnswer(t, f.vacancy, "story", []string{"I built payment systems."})
	chatPath := fmt.Sprintf("/api/vacancies/%d/application/chat", f.vacancy)

	sent := f.chat(t, f.vacancy, "Make it shorter, note-01. Write to rowan@example.test or +000 000 0000.", "team", []string{"Platform"})

	if len(sent) != 2 {
		t.Fatalf("messages after one send = %d, want the question and the reply", len(sent))
	}
	user, model := sent[0].(map[string]any), sent[1].(map[string]any)
	if user["role"] != "user" || user["field"] != "team" || model["role"] != "model" || model["text"] != "Done." || model["field"] != "team" {
		t.Errorf("messages = %v", sent)
	}
	if p, _ := model["proposal"].([]any); len(p) != 1 || p[0] != "Product" {
		t.Errorf("proposal = %v, want Product", model["proposal"])
	}
	got, _ := apiGet(t, f.base, chatPath)
	if again, _ := got["messages"].([]any); len(again) != 2 || again[0].(map[string]any)["position"] != float64(1) || again[1].(map[string]any)["position"] != float64(2) {
		t.Errorf("stored conversation = %v", got)
	}
	prompts := m.asked()
	if len(prompts) != 1 {
		t.Fatalf("model calls for one message = %d, want 1", len(prompts))
	}
	for _, want := range []string{"note-01", `["Platform"]`, "I built payment systems.", "Tell us about a hard project", "distributed systems", "unique-evidence-marker", "unique-qa-marker"} {
		if !strings.Contains(prompts[0], want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	for _, banned := range []string{"rowan@example.test", "+000 000 0000", "Rowan Quill", "linkedin.example.test"} {
		if strings.Contains(prompts[0], banned) {
			t.Errorf("prompt holds %q", banned)
		}
	}

	for i := 2; i <= 12; i++ {
		f.chat(t, f.vacancy, fmt.Sprintf("note-%02d", i), "story", []string{"I built payment systems."})
	}
	last := m.asked()[11]
	if strings.Contains(last, "note-01") || strings.Contains(last, "note-06") || !strings.Contains(last, "note-07") || !strings.Contains(last, "note-11") {
		t.Errorf("the prompt of the twelfth message must hold the ten messages before it and nothing older")
	}
	if all, _ := apiGet(t, f.base, chatPath); len(all["messages"].([]any)) != 24 {
		t.Errorf("stored messages = %d, want all 24 kept", len(all["messages"].([]any)))
	}
}

func TestSavingAnOpenAnswerWritesQuestionsAndAnswersAndAnEmptySaveRemovesNothing(t *testing.T) {
	f, _ := chatFix(t)
	f.shortlist(t)
	f.settled(t)

	f.saveAnswer(t, f.vacancy, "story", []string{"First version."})
	f.saveAnswer(t, f.vacancy, "story", []string{"Second version."})

	items := f.qaItems(t)
	if len(items) != 1 {
		t.Fatalf("Q&A items = %v, want one that the second save updated", items)
	}
	if it := items[0].(map[string]any); it["question"] != "Tell us about a hard project" || it["answer"] != "Second version." {
		t.Errorf("item = %v", it)
	}

	f.saveAnswer(t, f.vacancy, "story", []string{})

	if items := f.qaItems(t); len(items) != 1 || items[0].(map[string]any)["answer"] != "Second version." {
		t.Errorf("an empty save changed Q&A: %v", items)
	}
}

func TestAnAnswerNamingItsCompanyIsNotPastedIntoAnotherCompanysForm(t *testing.T) {
	f, _ := chatFix(t)
	acme := f.another(t, "https://jobs.ashbyhq.com/acme/1c2d3e4f-5061-4a7b-8c9d-0e1f2a3b4c5d", "Acme Inc")
	f.saveAnswer(t, acme, "story", []string{"I admire Acme's work and rebuilt its ledger."})

	f.shortlist(t)
	f.settled(t)
	other := f.vacancy
	again := f.another(t, "https://jobs.ashbyhq.com/acme/2d3e4f50-6172-4b8c-9dae-1f2a3b4c5d6e", "Acme Inc")

	if story := f.storyOf(t, other); story == nil || len(story["values"].([]any)) != 0 || story["source"] != "" {
		t.Errorf("another company's form got the answer written for Acme: %v", story)
	}
	if story := f.storyOf(t, again); story == nil || story["source"] != "qa" || story["values"].([]any)[0] != "I admire Acme's work and rebuilt its ledger." {
		t.Errorf("a second form at the same company = %v, want the saved answer from Q&A", story)
	}
}
