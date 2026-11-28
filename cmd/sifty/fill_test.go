package main

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func fillField(key, label, kind string, options ...string) map[string]any {
	opts := []map[string]string{}
	for _, o := range options {
		opts = append(opts, map[string]string{"value": o, "label": o})
	}
	return map[string]any{"key": key, "label": label, "kind": kind, "required": false, "options": opts}
}

func fillAt(t *testing.T, base, page string, fields ...map[string]any) map[string]any {
	t.Helper()
	out, status := apiSend(t, http.MethodPost, base, "/api/extension/fill", map[string]any{"url": page, "title": "Backend Engineer", "text": "A role.", "fields": fields})
	if status != http.StatusOK {
		t.Fatalf("fill %s: status %d: %v", page, status, out)
	}
	return out
}

func answerOf(t *testing.T, reply map[string]any, key string) (value, source string) {
	t.Helper()
	for _, raw := range reply["answers"].([]any) {
		a := raw.(map[string]any)
		if a["key"] == key {
			return a["value"].(string), a["source"].(string)
		}
	}
	return "", ""
}

func leftKeys(reply map[string]any) map[string]string {
	out := map[string]string{}
	for _, raw := range reply["left"].([]any) {
		l := raw.(map[string]any)
		out[l["key"].(string)] = l["reason"].(string)
	}
	return out
}

func myData(t *testing.T, base string) {
	t.Helper()
	out, status := apiSend(t, http.MethodPatch, base, "/api/me", map[string]any{"email": "rowan@example.test", "workAuthorizations": []map[string]any{{"country": "DE", "status": "eligible"}}})
	if status != http.StatusOK {
		t.Fatalf("my data: status %d: %v", status, out)
	}
}

func TestExtensionFillAnswersFromMyDataForTheVacancyOfAPageAndNeverForAnotherPage(t *testing.T) {
	d := openTestDB(t)
	base := newRouteServer(t, d, runner.New(d, nil, time.Now, nil))
	myData(t, base)
	vacancy := insertFullVacancy(t, d, "https://boards.example.test/acme/jobs/4001", "Backend Engineer", "Acme")
	insertFullVacancy(t, d, "https://boards.example.test/acme/jobs/4002", "Data Engineer", "Acme")
	fields := []map[string]any{
		fillField("email", "Email", "email"),
		fillField("auth", "Are you legally authorized to work in Germany?", "yes_no", "Yes", "No"),
		fillField("story", "Tell us about a hard project", "long_text"),
	}

	got := fillAt(t, base, "https://www.boards.example.test/acme/jobs/4001/?utm_source=feed&gh_src=x", fields...)
	if v := got["vacancy"].(map[string]any); int64(v["id"].(float64)) != vacancy || got["form"] != float64(vacancy) || got["state"] != "done" {
		t.Fatalf("reply = %v, want the form of vacancy %d", got, vacancy)
	}
	if v, s := answerOf(t, got, "email"); v != "rowan@example.test" || s != "my_data" {
		t.Errorf("email = %q from %q", v, s)
	}
	if v, _ := answerOf(t, got, "auth"); v != "Yes" {
		t.Errorf("work authorization = %q, want Yes", v)
	}
	if reason := leftKeys(got)["story"]; reason != "Sifty has no answer for this yet" {
		t.Errorf("story left with %q", reason)
	}
	again, status := apiGet(t, base, fmt.Sprintf("/api/extension/fill/%d", vacancy))
	if status != http.StatusOK || !reflect.DeepEqual(again, got) {
		t.Errorf("GET fill/%d = %v (status %d), want the reply of the fill %v", vacancy, again, status, got)
	}

	app, _ := apiGet(t, base, fmt.Sprintf("/api/vacancies/%d/application", vacancy))
	var story map[string]any
	for _, raw := range app["fields"].([]any) {
		if f := raw.(map[string]any); f["key"] == "story" {
			story = f
		}
	}
	patched, status := apiSend(t, http.MethodPatch, base, fmt.Sprintf("/api/vacancies/%d/application/fields/story", vacancy), map[string]any{"values": []string{"Mine"}, "revision": story["revision"]})
	if status != http.StatusOK {
		t.Fatalf("PATCH story: status %d: %v", status, patched)
	}

	for _, page := range []string{"https://boards.example.test/acme/jobs/4999", "https://boards.example.test/acme/", "https://boards.example.test/acme/jobs/4001/apply"} {
		other := fillAt(t, base, page, fields...)
		if other["vacancy"] != nil || other["form"] != float64(0) {
			t.Errorf("fill from %s: vacancy %v form %v, want none", page, other["vacancy"], other["form"])
		}
		if v, _ := answerOf(t, other, "email"); v != "rowan@example.test" {
			t.Errorf("fill from %s: email = %q, want the My data answer", page, v)
		}
		if v, s := answerOf(t, other, "story"); v != "Mine" || s != "qa" {
			t.Errorf("fill from %s: story = %q from %q, want the saved answer of Questions & Answers and never the vacancy's own edit", page, v, s)
		}
	}
	if kept, _ := apiGet(t, base, fmt.Sprintf("/api/extension/fill/%d", vacancy)); !reflect.DeepEqual(kept["answers"], append(got["answers"].([]any)[:2:2], map[string]any{"key": "story", "value": "Mine", "values": []any{"Mine"}, "source": "user", "note": ""})) {
		t.Errorf("stored answers of vacancy %d changed: %v", vacancy, kept["answers"])
	}
	if out, status := apiGet(t, base, "/api/extension/fill/0"); status != http.StatusNotFound || out["error"] != "Nothing stored for this page." {
		t.Errorf("GET fill/0 = %v (status %d)", out, status)
	}
	if err := formread.Save(context.Background(), d, vacancy, formread.Form{}, formread.StateUnsupported, "", time.Now()); err != nil {
		t.Fatalf("a preparation that finds no template: %v", err)
	}
	if kept, _ := apiGet(t, base, fmt.Sprintf("/api/vacancies/%d/application", vacancy)); len(kept["fields"].([]any)) != 1 {
		t.Errorf("a preparation without a template dropped the form the page sent: %v", kept)
	}
}

type answerFix struct {
	formFix
}

func newAnswerFix(t *testing.T) answerFix {
	t.Helper()
	f := newFormFix(t, "https://job-boards.greenhouse.io/stackworks/jobs/4001", "https://boards-api.greenhouse.io/v1/boards/stackworks/jobs/4001?")
	f.site.answer(200, greenhouseFirst)
	myData(t, f.base)
	if out, status := apiSend(t, http.MethodPost, f.base, "/api/qa", map[string]any{"question": "Why Stackworks?", "answer": "The product."}); status != http.StatusOK {
		t.Fatalf("qa: status %d: %v", status, out)
	}
	f.shortlist(t)
	f.settled(t)
	return answerFix{f}
}

func (f answerFix) field(t *testing.T, key string) map[string]any {
	t.Helper()
	for _, raw := range f.application(t)["fields"].([]any) {
		if field := raw.(map[string]any); field["key"] == key {
			return field
		}
	}
	t.Fatalf("no field %s", key)
	return nil
}

func (f answerFix) edit(t *testing.T, key string, value string, revision any) (map[string]any, int) {
	t.Helper()
	return apiSend(t, http.MethodPatch, f.base, fmt.Sprintf("/api/vacancies/%d/application/fields/%s", f.vacancy, key), map[string]any{"values": []string{value}, "revision": revision})
}

func TestEditedAnswerSurvivesAPartialFillAFullFillAndANewPreparation(t *testing.T) {
	f := newAnswerFix(t)
	page := "https://job-boards.greenhouse.io/stackworks/jobs/4001"
	if got := f.field(t, "question_1"); got["source"] != "qa" || got["values"].([]any)[0] != "The product." {
		t.Fatalf("prepared answer = %v, want the Questions & Answers one", got)
	}
	if out, status := f.edit(t, "question_1", "Mine", f.field(t, "question_1")["revision"]); status != http.StatusOK {
		t.Fatalf("edit: status %d: %v", status, out)
	}
	assertKept := func(step string) {
		t.Helper()
		if got := f.field(t, "question_1"); got["source"] != "user" || !reflect.DeepEqual(got["values"], []any{"Mine"}) {
			t.Errorf("after %s: answer = %v, want the edit kept as written by the user", step, got)
		}
	}

	partial := fillAt(t, f.base, page, fillField("notice", "Notice period", "short_text"))
	if _, ok := leftKeys(partial)["notice"]; !ok || len(partial["answers"].([]any)) != 0 {
		t.Errorf("partial fill = %v", partial)
	}
	assertKept("a fill that does not post the field")

	full := fillAt(t, f.base, page, fillField("why", "Why Stackworks?", "long_text"))
	if v, s := answerOf(t, full, "why"); v != "Mine" || s != "user" {
		t.Errorf("fill answered %q from %q, want the edit", v, s)
	}
	assertKept("a fill that posts the field")

	if out, status := apiSend(t, http.MethodPost, f.base, fmt.Sprintf("/api/vacancies/%d/application/retry", f.vacancy), nil); status != http.StatusOK {
		t.Fatalf("retry: status %d: %v", status, out)
	}
	f.settled(t)
	assertKept("a new preparation")
}

func TestEditWithAnOldRevisionIsRefused(t *testing.T) {
	f := newAnswerFix(t)
	old := f.field(t, "question_1")["revision"]
	if out, status := f.edit(t, "question_1", "First", old); status != http.StatusOK {
		t.Fatalf("first edit: status %d: %v", status, out)
	}
	out, status := f.edit(t, "question_1", "Second", old)
	if status != http.StatusConflict || out["error"] != "This answer was changed elsewhere" {
		t.Errorf("edit with revision %v = %d %v, want 409 \"This answer was changed elsewhere\"", old, status, out)
	}
	if got := f.field(t, "question_1")["values"]; !reflect.DeepEqual(got, []any{"First"}) {
		t.Errorf("answer = %v, want First", got)
	}
}
