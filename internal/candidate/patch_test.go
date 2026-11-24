package candidate_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func listLen(m map[string]any, key string) int {
	l, _ := m[key].([]any)
	return len(l)
}

func seedMe(t *testing.T, ts *httptest.Server) {
	t.Helper()
	_, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"displayName": "Sam Rivera",
		"email":       "sam@example.com",
		"skills": []any{
			map[string]any{"name": "Go", "level": "Expert"},
			map[string]any{"name": "SQL", "level": "Proficient"},
		},
		"languages": []any{
			map[string]any{"name": "English", "level": "Native"},
		},
		"availability":    map[string]any{"noticeValue": 2, "noticeUnit": "week"},
		"currentLocation": map[string]any{"country": "FR", "city": "Lyon"},
	})
	if status != http.StatusOK {
		t.Fatalf("seed me: status %d", status)
	}
}

func seedProfile(t *testing.T, ts *httptest.Server) {
	t.Helper()
	_, status := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"name":        "Main",
		"targetRoles": []any{"Backend engineer", "Platform engineer"},
		"keywords":    []any{"golang", "sqlite"},
		"compensation": map[string]any{
			"cashMin": 50000, "currency": "EUR", "period": "year",
		},
		"otherRequirements": "No night shifts",
	})
	if status != http.StatusOK {
		t.Fatalf("seed profile: status %d", status)
	}
}

func ownOf(t *testing.T, ts *httptest.Server) map[string]any {
	t.Helper()
	body, status := get(t, ts, "/api/profiles/1")
	if status != http.StatusOK {
		t.Fatalf("GET /api/profiles/1: status %d", status)
	}
	own, _ := body["own"].(map[string]any)
	return own
}

func TestMePatchKeepsFieldsTheRequestDoesNotName(t *testing.T) {
	ts := newServer(t, openTestDB(t))
	seedMe(t, ts)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"skills":      nil,
		"displayName": "Sam Rivera-Lee",
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH: status %d, body %v", status, out)
	}
	if n := listLen(out, "skills"); n != 0 {
		t.Errorf("answer skills = %d entries, want 0", n)
	}
	stored, _ := get(t, ts, "/api/me")
	if n := listLen(stored, "skills"); n != 0 {
		t.Errorf("stored skills = %d entries, want 0", n)
	}
	if stored["displayName"] != "Sam Rivera-Lee" {
		t.Errorf("displayName = %v, want the new value", stored["displayName"])
	}
	if n := listLen(stored, "languages"); n != 1 {
		t.Errorf("languages = %d entries, want the stored 1", n)
	}
	if stored["email"] != "sam@example.com" {
		t.Errorf("email = %v, want the stored value", stored["email"])
	}
	if stored["availability"] == nil {
		t.Error("availability was not named and must be kept")
	}
}

func TestMePatchClearingARequiredCountryIsRefusedAndNothingIsStored(t *testing.T) {
	ts := newServer(t, openTestDB(t))
	seedMe(t, ts)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"currentLocation": map[string]any{"country": "", "city": "Lyon"},
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %v", status, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "Current location: choose a country.") {
		t.Errorf("message = %v, want the validation message", out)
	}
	stored, _ := get(t, ts, "/api/me")
	loc, _ := stored["currentLocation"].(map[string]any)
	if loc["country"] != "FR" {
		t.Errorf("stored country = %v, want FR", loc["country"])
	}
}

func TestProfilePatchKeepsFieldsTheRequestDoesNotName(t *testing.T) {
	ts := newServer(t, openTestDB(t))
	seedProfile(t, ts)

	out, status := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"targetRoles": nil,
		"headline":    "Backend engineer",
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH: status %d, body %v", status, out)
	}
	answered, _ := out["own"].(map[string]any)
	if n := listLen(answered, "targetRoles"); n != 0 {
		t.Errorf("answer targetRoles = %d entries, want 0", n)
	}
	own := ownOf(t, ts)
	if n := listLen(own, "targetRoles"); n != 0 {
		t.Errorf("stored targetRoles = %d entries, want 0", n)
	}
	if own["headline"] != "Backend engineer" {
		t.Errorf("headline = %v, want the new value", own["headline"])
	}
	if n := listLen(own, "keywords"); n != 2 {
		t.Errorf("keywords = %d entries, want the stored 2", n)
	}
	if own["name"] != "Main" {
		t.Errorf("name = %v, want the stored value", own["name"])
	}
	if own["otherRequirements"] != "No night shifts" {
		t.Errorf("otherRequirements = %v, want the stored value", own["otherRequirements"])
	}
	if own["compensation"] == nil {
		t.Error("compensation was not named and must be kept")
	}
}
