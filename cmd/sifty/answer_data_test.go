package main

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/answer"
	"github.com/sifty-works/sifty-server/internal/formread"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func TestAnswersUseTheActiveProfileCompensationTheLegalNameAndFreeMovementOfWorkAuthorization(t *testing.T) {
	d := openTestDB(t)
	base := newRouteServer(t, d, runner.New(d, nil, time.Now, slog.Default()))
	send := func(method, path string, payload any) map[string]any {
		t.Helper()
		out, status := apiSend(t, method, base, path, payload)
		if status != http.StatusOK {
			t.Fatalf("%s %s: status %d: %v", method, path, status, out)
		}
		return out
	}
	send(http.MethodPatch, "/api/me", map[string]any{"legalName": "Rowan Quill", "displayName": "Ro", "workAuthorizations": []map[string]any{{"country": "DE", "status": "eligible"}, {"country": "CA", "status": "need_visa"}}, "demographics": map[string]any{"gender": "man"}})
	send(http.MethodPatch, "/api/profiles/1", map[string]any{"compensation": map[string]any{"cashMin": 50000, "cashTarget": 60000, "currency": "EUR", "period": "year"}})
	second := send(http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})
	id := int64(second["id"].(float64))
	send(http.MethodPatch, "/api/profiles/2", map[string]any{"compensation": map[string]any{"cashMin": 4000, "cashTarget": 5000, "currency": "USD", "period": "month"}})
	send(http.MethodPut, "/api/active-profile", map[string]any{"id": id})
	send(http.MethodPost, "/api/qa", map[string]any{"question": "Why do you want to work here?", "answer": "I like the product."})

	out, err := answer.Coded{Data: answerData{d}}.Answer(context.Background(), answer.Vacancy{}, formread.Form{Fields: []formread.Field{
		{ID: "salary", Label: "Salary expectations", Kind: "short_text"},
		{ID: "name", Label: "Full name", Kind: "short_text"},
		{ID: "why", Label: "Why do you want to work here?", Kind: "long_text"},
		{ID: "spain", Label: "Are you legally authorized to work in Spain?", Kind: "yes_no"},
		{ID: "us", Label: "Are you legally authorized to work in the U.S.?", Kind: "yes_no"},
		{ID: "canada", Label: "Are you legally authorized to work in Canada?", Kind: "yes_no"},
		{ID: "ussp", Label: "Will you require visa sponsorship to work in the United States?", Kind: "yes_no"},
		{ID: "gender", Label: "Gender", Kind: "choice", Options: []string{"Female", "Male"}},
		{ID: "based", Label: "Are you authorized to work in the country in which this job is based?", Kind: "yes_no"},
	}}, nil)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	for id, want := range map[string]string{"salary": "5000 USD per month", "name": "Rowan Quill", "why": "I like the product.", "spain": "Yes", "us": "No", "canada": "No", "ussp": "Yes", "gender": "Male"} {
		if got := out[id].Values; len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want %q", id, got, want)
		}
	}
	if got := out["based"].Values; len(got) != 0 {
		t.Errorf("a label that names no place answered %v from several authorizations", got)
	}
}
