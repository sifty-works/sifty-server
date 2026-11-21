package candidate_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/server"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func newServer(t *testing.T, d *db.DB, changed ...func()) *httptest.Server {
	return newServerWithFiles(t, d, "", changed...)
}

func newServerWithFiles(t *testing.T, d *db.DB, filesDir string, changed ...func()) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := server.New(server.Options{
		Version:           "test",
		Port:              9999,
		Logger:            logger,
		BinaryUploadPaths: []string{"/api/me/photo"},
	}, func(mux *http.ServeMux) {
		var notify func()
		if len(changed) > 0 {
			notify = changed[0]
		}
		candidate.RegisterRoutes(mux, d, filesDir, nil, logger, notify, nil)
	})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, ts *httptest.Server, path string) (map[string]any, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	req.Host = "127.0.0.1:9999"
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	return body, resp.StatusCode
}

func send(t *testing.T, ts *httptest.Server, method, path string, payload any) (map[string]any, int) {
	t.Helper()
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
	req.Host = "127.0.0.1:9999"
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}

func TestMeInitiallyEmpty(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	body, status := get(t, ts, "/api/me")
	if status != http.StatusOK {
		t.Fatalf("GET /api/me: status %d", status)
	}
	if body == nil {
		t.Fatal("GET /api/me: nil body")
	}
}

func TestMeIdentitySectionRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"displayName": "Alex Taylor",
		"email":       "alex@example.com",
		"phone":       "+1 555 1234",
		"linkedin":    "https://linkedin.com/in/example",
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/me: status %d, body: %v", status, out)
	}
	if out["displayName"] != "Alex Taylor" {
		t.Errorf("displayName = %v, want Alex Taylor", out["displayName"])
	}
	if out["email"] != "alex@example.com" {
		t.Errorf("email = %v, want alex@example.com", out["email"])
	}

	body, _ := get(t, ts, "/api/me")
	if body["displayName"] != "Alex Taylor" {
		t.Errorf("GET after PATCH: displayName = %v", body["displayName"])
	}
}

func TestMeSkillsSectionRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"skills": []any{
			map[string]any{"name": "Go", "level": "Expert"},
			map[string]any{"name": "Python", "level": "Proficient"},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/me skills: status %d", status)
	}
	skills, _ := out["skills"].([]any)
	if len(skills) != 2 {
		t.Errorf("skills: got %d, want 2", len(skills))
	}
}

func TestMeExperienceSectionRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"experience": []any{
			map[string]any{
				"company": "Acme Corp",
				"title":   "Senior Engineer",
				"current": true,
			},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/me experience: status %d", status)
	}
	exp, _ := out["experience"].([]any)
	if len(exp) != 1 {
		t.Fatalf("experience: got %d, want 1", len(exp))
	}
}

func TestMeLanguagesSectionRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"languages": []any{
			map[string]any{"name": "English", "level": "Native"},
			map[string]any{"name": "Spanish", "level": "B2"},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/me languages: status %d", status)
	}
	langs, _ := out["languages"].([]any)
	if len(langs) != 2 {
		t.Errorf("languages: got %d, want 2", len(langs))
	}
}

func TestMeAvailabilitySectionRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"availability": map[string]any{
			"noticeValue": 2,
			"noticeUnit":  "week",
			"startDate":   "2027-03-01",
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/me availability: status %d", status)
	}
	avail, _ := out["availability"].(map[string]any)
	if avail["noticeUnit"] != "week" {
		t.Errorf("noticeUnit = %v, want week", avail["noticeUnit"])
	}
}

func TestMeLocationSectionRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"currentLocation": map[string]any{"country": "DE", "city": "Berlin"},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/me location: status %d", status)
	}
	loc, _ := out["currentLocation"].(map[string]any)
	if loc["country"] != "DE" {
		t.Errorf("country = %v, want DE", loc["country"])
	}
}

func TestMeWorkAuthorizationsSectionRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"workAuthorizations": []any{
			map[string]any{"country": "DE", "status": "eligible"},
			map[string]any{"country": "FR", "status": "need_visa"},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/me workAuthorizations: status %d", status)
	}
	auths, _ := out["workAuthorizations"].([]any)
	if len(auths) != 2 {
		t.Errorf("workAuthorizations: got %d, want 2", len(auths))
	}
}

func TestMeDemographicsSectionRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"demographics": map[string]any{
			"gender":    "prefer_not_to_say",
			"ethnicity": "prefer_not_to_say",
		},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/me demographics: status %d", status)
	}
	demo, _ := out["demographics"].(map[string]any)
	if demo == nil {
		t.Fatal("demographics missing from response")
	}
}

func TestMePatchDoesNotWipeUnsentSections(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"skills": []any{map[string]any{"name": "Go", "level": "Expert"}},
	})

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"displayName": "Morgan Lee",
	})
	if status != http.StatusOK {
		t.Fatalf("second PATCH: status %d", status)
	}
	skills, _ := out["skills"].([]any)
	if len(skills) != 1 {
		t.Errorf("skills after identity patch: got %d, want 1", len(skills))
	}
}

func TestSkillWithoutNameIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"skills": []any{map[string]any{"name": "", "level": "Expert"}},
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("empty skill name: status %d, want 422", status)
	}
}

func TestSkillInBothListsIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"skills":       []any{map[string]any{"name": "Go", "level": "Expert"}},
		"absentSkills": []any{map[string]any{"name": "Go"}},
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("skill in both lists: status %d, want 422; body: %v", status, out)
	}
}

func TestLanguageWithoutNameIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"languages": []any{map[string]any{"name": "", "level": "B2"}},
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("language without name: status %d, want 422", status)
	}
}

func TestAvailabilityNoticeMissingUnitIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"availability": map[string]any{"noticeValue": 3},
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("notice without unit: status %d, want 422", status)
	}
}

func TestAvailabilityNoticeZeroIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"availability": map[string]any{"noticeValue": 0, "noticeUnit": "week"},
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("notice value 0: status %d, want 422", status)
	}
}

func TestWorkAuthorizationMissingCountryIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"workAuthorizations": []any{map[string]any{"status": "eligible"}},
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("authorization without country: status %d, want 422", status)
	}
}

func TestProfileListHasOneDefaultProfile(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	body, status := get(t, ts, "/api/profiles")
	if status != http.StatusOK {
		t.Fatalf("GET /api/profiles: status %d", status)
	}
	profiles, _ := body["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	p := profiles[0].(map[string]any)
	if p["name"] != "Profile 1" {
		t.Errorf("default profile name = %q, want %q", p["name"], "Profile 1")
	}
	if p["active"] != true {
		t.Error("default profile should be active")
	}
}

func TestProfileDetailReturnsExpectedFields(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	body, status := get(t, ts, "/api/profiles/1")
	if status != http.StatusOK {
		t.Fatalf("GET /api/profiles/1: status %d", status)
	}
	for _, field := range []string{"id", "name", "own", "effective", "headline"} {
		if body[field] == nil {
			t.Errorf("profile detail missing field %q", field)
		}
	}
}

func TestCreateProfileBecomesActive(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{
		"name": "Remote Work",
	})
	if status != http.StatusOK {
		t.Fatalf("POST /api/profiles: status %d, body: %v", status, out)
	}
	if out["name"] != "Remote Work" {
		t.Errorf("created profile name = %v, want Remote Work", out["name"])
	}
	if out["active"] != true {
		t.Error("newly created profile should be active")
	}

	listBody, _ := get(t, ts, "/api/profiles")
	profiles, _ := listBody["profiles"].([]any)
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles after create, got %d", len(profiles))
	}

	var activeCount int
	for _, p := range profiles {
		if pm, ok := p.(map[string]any); ok && pm["active"] == true {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("expected exactly 1 active profile, got %d", activeCount)
	}
}

func TestSavingMyDataOrAProfileReportsAChange(t *testing.T) {
	calls := 0
	ts := newServer(t, openTestDB(t), func() { calls++ })
	get(t, ts, "/api/profiles/1")
	for i, req := range []struct{ method, path string }{
		{http.MethodPatch, "/api/me"}, {http.MethodPost, "/api/profiles"}, {http.MethodPatch, "/api/profiles/1"},
	} {
		if _, status := send(t, ts, req.method, req.path, map[string]any{"name": "Remote Work"}); status != http.StatusOK {
			t.Fatalf("%s %s: status %d", req.method, req.path, status)
		}
		if calls != i+1 {
			t.Errorf("after %s %s: %d changes reported, want %d", req.method, req.path, calls, i+1)
		}
	}
}

func TestCreateProfileWithEmptyNameIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": ""})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("empty profile name: status %d, want 422", status)
	}
}

func TestPatchProfileName(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"name": "Main",
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/profiles/1: status %d", status)
	}
	if out["name"] != "Main" {
		t.Errorf("profile name = %v, want Main", out["name"])
	}
}

func TestPatchProfileEmptyNameIsIgnored(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{"name": "Named"})

	out, status := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{"name": ""})
	if status != http.StatusOK {
		t.Fatalf("PATCH with empty name: status %d", status)
	}
	if out["name"] != "Named" {
		t.Errorf("after empty-name patch, name = %v, want Named", out["name"])
	}
}

func TestPatchProfileHeadlineTooLongIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	long := strings.Repeat("x", 161)
	_, status := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"headline": long,
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("long headline: status %d, want 422", status)
	}
}

func TestPatchProfileHeadlineMultilineIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"headline": "line1\nline2",
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("multiline headline: status %d, want 422", status)
	}
}

func TestPatchProfileCompensationWithoutCurrencyIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, status := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"compensation": map[string]any{
			"cashMin": 50000.0,
		},
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("compensation without currency: status %d, want 422; body: %v", status, out)
	}
}

func TestPatchProfileCompensationTargetBelowMinIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"compensation": map[string]any{
			"cashMin":    80000.0,
			"cashTarget": 70000.0,
			"currency":   "EUR",
		},
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("target below min: status %d, want 422", status)
	}
}

func TestDeleteFirstProfileIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodDelete, "/api/profiles/1", struct{}{})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("delete first profile: status %d, want 422", status)
	}
}

func TestDeletedProfileLeavesTheFirstActive(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, _ := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})
	secondID := int64(out["id"].(float64))

	deleted, status := send(t, ts, http.MethodDelete, fmt.Sprintf("/api/profiles/%d", secondID), struct{}{})
	if status != http.StatusOK {
		t.Fatalf("delete second profile: status %d, body: %v", status, deleted)
	}
	remaining, _ := deleted["profiles"].([]any)
	if len(remaining) != 1 || remaining[0].(map[string]any)["id"].(float64) != 1 {
		t.Errorf("profiles in the delete response = %v, want only profile 1", remaining)
	}

	listOut, _ := get(t, ts, "/api/profiles")
	profiles, _ := listOut["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	p := profiles[0].(map[string]any)
	if p["active"] != true {
		t.Error("first profile should be active after deleting the active second profile")
	}
}

func TestActiveProfileSwitching(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	out, _ := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})
	secondID := int64(out["id"].(float64))

	listOut, status := send(t, ts, http.MethodPut, "/api/active-profile", map[string]any{"id": 1})
	if status != http.StatusOK {
		t.Fatalf("PUT /api/active-profile: status %d", status)
	}

	profiles, _ := listOut["profiles"].([]any)
	for _, p := range profiles {
		pm := p.(map[string]any)
		id := int64(pm["id"].(float64))
		active, _ := pm["active"].(bool)
		if id == 1 && !active {
			t.Error("profile 1 should be active after switch")
		}
		if id == secondID && active {
			t.Errorf("profile %d should not be active after switching to profile 1", secondID)
		}
	}
}

func TestActiveProfileUsedWhenListHasNoProfileParam(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})

	send(t, ts, http.MethodPut, "/api/active-profile", map[string]any{"id": 1})

	listOut, _ := get(t, ts, "/api/profiles")
	profiles, _ := listOut["profiles"].([]any)
	for _, p := range profiles {
		pm := p.(map[string]any)
		id := int64(pm["id"].(float64))
		active, _ := pm["active"].(bool)
		if id == 1 && !active {
			t.Error("profile 1 should be marked active in list")
		}
	}
}

func TestInheritanceEmptyFieldFilledFromFirstProfile(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"compensation": map[string]any{
			"cashMin":  60000.0,
			"currency": "EUR",
			"period":   "year",
			"grossNet": "gross",
		},
	})

	out, _ := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})
	secondID := int64(out["id"].(float64))

	detail, status := get(t, ts, fmt.Sprintf("/api/profiles/%d", secondID))
	if status != http.StatusOK {
		t.Fatalf("GET profile detail: status %d", status)
	}

	inherited, _ := detail["inherited"].(map[string]any)
	if inherited == nil {
		t.Fatal("second profile should have an inherited map")
	}
	if _, ok := inherited["compensation"]; !ok {
		t.Error("compensation should be listed as inherited")
	}

	effective, _ := detail["effective"].(map[string]any)
	if effective["compensation"] == nil {
		t.Error("effective compensation should be non-nil (inherited from first)")
	}

	own, _ := detail["own"].(map[string]any)
	if own["compensation"] != nil {
		t.Error("own compensation should be nil — not stored on second profile")
	}
}

func TestInheritanceStoredValueOverridesFirstProfile(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"compensation": map[string]any{
			"cashMin":  60000.0,
			"currency": "EUR",
			"period":   "year",
			"grossNet": "gross",
		},
	})

	out, _ := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})
	secondID := int64(out["id"].(float64))

	send(t, ts, http.MethodPatch, fmt.Sprintf("/api/profiles/%d", secondID), map[string]any{
		"compensation": map[string]any{
			"cashMin":  90000.0,
			"currency": "USD",
			"period":   "year",
			"grossNet": "gross",
		},
	})

	detail, _ := get(t, ts, fmt.Sprintf("/api/profiles/%d", secondID))

	inherited, _ := detail["inherited"].(map[string]any)
	if _, ok := inherited["compensation"]; ok {
		t.Error("compensation must not be inherited once stored on second profile")
	}

	effective, _ := detail["effective"].(map[string]any)
	comp, _ := effective["compensation"].(map[string]any)
	if comp["currency"] != "USD" {
		t.Errorf("effective compensation currency = %v, want USD", comp["currency"])
	}
}

func TestInheritanceUnchangedInheritedFieldRemainsInherited(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"engagement": []string{"employment"},
	})

	out, _ := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})
	secondID := int64(out["id"].(float64))

	send(t, ts, http.MethodPatch, fmt.Sprintf("/api/profiles/%d", secondID), map[string]any{
		"name": "Second Updated",
	})

	detail, _ := get(t, ts, fmt.Sprintf("/api/profiles/%d", secondID))
	inherited, _ := detail["inherited"].(map[string]any)
	if _, ok := inherited["engagement"]; !ok {
		t.Error("engagement should still be inherited after an unrelated patch")
	}
}

func TestInheritancePatchingInheritedValueUnchangedKeepsItInherited(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"compensation": map[string]any{"cashMin": 1000, "currency": "EUR", "period": "month"},
	})

	out, _ := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})
	secondID := int64(out["id"].(float64))

	detail, _ := get(t, ts, fmt.Sprintf("/api/profiles/%d", secondID))
	effective, _ := detail["effective"].(map[string]any)
	effectiveComp := effective["compensation"]

	patched, status := send(t, ts, http.MethodPatch, fmt.Sprintf("/api/profiles/%d", secondID), map[string]any{
		"compensation": effectiveComp,
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH with inherited value: status %d", status)
	}
	inherited, _ := patched["inherited"].(map[string]any)
	if _, ok := inherited["compensation"]; !ok {
		t.Error("compensation must still be inherited after patching with the same value")
	}
	ownData, _ := patched["own"].(map[string]any)
	if ownData["compensation"] != nil {
		t.Error("own compensation must remain nil when patched with the inherited value")
	}

	patched2, status := send(t, ts, http.MethodPatch, fmt.Sprintf("/api/profiles/%d", secondID), map[string]any{
		"compensation": map[string]any{"cashMin": 2000, "currency": "USD", "period": "month"},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH with different value: status %d", status)
	}
	inherited2, _ := patched2["inherited"].(map[string]any)
	if _, ok := inherited2["compensation"]; ok {
		t.Error("compensation must not be inherited after storing a different own value")
	}
}

func TestPatchAllNullCompensationOnNonFirstProfileIsInherited(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, code := send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"compensation": map[string]any{
			"cashMin": 50000, "cashTarget": 70000,
			"currency": "USD", "period": "year",
		},
	})
	if code != http.StatusOK {
		t.Fatalf("PATCH profile 1 compensation: status %d", code)
	}

	body, code := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Alt"})
	if code != http.StatusOK {
		t.Fatalf("POST profiles: status %d", code)
	}
	id2 := int64(body["id"].(float64))

	_, code = send(t, ts, http.MethodPatch, fmt.Sprintf("/api/profiles/%d", id2), map[string]any{
		"compensation": map[string]any{
			"cashMin": nil, "cashTarget": nil, "equityOnTop": nil,
		},
	})
	if code != http.StatusOK {
		t.Fatalf("PATCH profile 2 all-null compensation: status %d", code)
	}

	got, code := get(t, ts, fmt.Sprintf("/api/profiles/%d", id2))
	if code != http.StatusOK {
		t.Fatalf("GET profile 2: status %d", code)
	}
	inheritedRaw, hasInherited := got["inherited"]
	if !hasInherited || inheritedRaw == nil {
		t.Fatal("profile 2 inherited: nil — empty compensation should have been removed and compensation stays inherited")
	}
	inherited, ok := inheritedRaw.(map[string]any)
	if !ok {
		t.Fatalf("profile 2 inherited: unexpected type %T", inheritedRaw)
	}
	if _, ok := inherited["compensation"]; !ok {
		t.Errorf("profile 2 compensation should be inherited after all-null patch; inherited=%v", inherited)
	}
	own, _ := got["own"].(map[string]any)
	if own["compensation"] != nil {
		t.Errorf("own compensation should be absent after all-null patch, got %v", own["compensation"])
	}
}

func TestTargetRolesAndKeywordsAreNotInherited(t *testing.T) {
	for _, tc := range []struct {
		field  string
		values []string
	}{
		{"targetRoles", []string{"Engineer", "Developer"}},
		{"keywords", []string{"remote", "senior"}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			d := openTestDB(t)
			ts := newServer(t, d)

			send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{tc.field: tc.values})

			out, _ := send(t, ts, http.MethodPost, "/api/profiles", map[string]any{"name": "Second"})
			secondID := int64(out["id"].(float64))

			detail, _ := get(t, ts, fmt.Sprintf("/api/profiles/%d", secondID))
			inherited, _ := detail["inherited"].(map[string]any)
			if _, ok := inherited[tc.field]; ok {
				t.Errorf("%s must not be inherited from the first profile", tc.field)
			}
		})
	}
}

func TestHeadlineFallbackWhenNoData(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	detail, _ := get(t, ts, "/api/profiles/1")
	headline, _ := detail["headline"].(string)
	if headline != "Candidate profile" {
		t.Errorf("headline with no data = %q, want %q", headline, "Candidate profile")
	}
}

func TestHeadlineBuiltFromCurrentExperience(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"experience": []any{
			map[string]any{
				"company": "Globex",
				"title":   "Senior Engineer",
				"current": true,
			},
		},
		"currentLocation": map[string]any{"country": "DE", "city": "Berlin"},
	})

	detail, _ := get(t, ts, "/api/profiles/1")
	headline, _ := detail["headline"].(string)
	if headline == "Candidate profile" {
		t.Error("headline should not fall back when experience is set")
	}
	if !strings.Contains(headline, "Senior Engineer") {
		t.Errorf("headline should contain the job title, got %q", headline)
	}
}

func TestExplicitHeadlineTakesPrecedence(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	send(t, ts, http.MethodPatch, "/api/profiles/1", map[string]any{
		"headline": "Full-stack engineer",
	})

	send(t, ts, http.MethodPatch, "/api/me", map[string]any{
		"experience": []any{
			map[string]any{"company": "Acme", "title": "CTO", "current": true},
		},
	})

	detail, _ := get(t, ts, "/api/profiles/1")
	headline, _ := detail["headline"].(string)
	if headline != "Full-stack engineer" {
		t.Errorf("headline = %q, want %q", headline, "Full-stack engineer")
	}
}

func makePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func sendBinary(t *testing.T, ts *httptest.Server, method, path, contentType string, body []byte) int {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	req.Host = "127.0.0.1:9999"
	req.Header.Set("Content-Type", contentType)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestPhotoPNGAcceptedAndRemoved(t *testing.T) {
	d := openTestDB(t)
	filesDir := filepath.Join(t.TempDir(), "files")
	ts := newServerWithFiles(t, d, filesDir)

	pngBytes := makePNG()

	status := sendBinary(t, ts, http.MethodPut, "/api/me/photo", "image/png", pngBytes)
	if status != http.StatusNoContent {
		t.Fatalf("PUT /api/me/photo: status %d, want 204", status)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/me/photo", nil)
	req.Host = "127.0.0.1:9999"
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /api/me/photo: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/me/photo: status %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(body, pngBytes) {
		t.Error("GET /api/me/photo: returned bytes do not match uploaded bytes")
	}

	_, deleteStatus := send(t, ts, http.MethodDelete, "/api/me/photo", struct{}{})
	if deleteStatus != http.StatusNoContent {
		t.Errorf("DELETE /api/me/photo: status %d, want 204", deleteStatus)
	}

	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/me/photo", nil)
	req2.Host = "127.0.0.1:9999"
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("GET /api/me/photo after delete: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("GET after delete: status %d, want 404", resp2.StatusCode)
	}
}

func TestPhotoRenamedTextFileIsRejected(t *testing.T) {
	d := openTestDB(t)
	filesDir := filepath.Join(t.TempDir(), "files")
	ts := newServerWithFiles(t, d, filesDir)

	textBytes := []byte("This is a text file, not a PNG image.")
	status := sendBinary(t, ts, http.MethodPut, "/api/me/photo", "image/png", textBytes)
	if status != http.StatusUnprocessableEntity {
		t.Errorf("text file as photo: status %d, want 422", status)
	}
}

func TestPhoto3MiBIsRejected(t *testing.T) {
	d := openTestDB(t)
	filesDir := filepath.Join(t.TempDir(), "files")
	ts := newServerWithFiles(t, d, filesDir)

	big := make([]byte, 3<<20)
	status := sendBinary(t, ts, http.MethodPut, "/api/me/photo", "image/png", big)
	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("3 MiB photo: status %d, want 413", status)
	}
}

func TestQARoundTrip(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	listOut, status := get(t, ts, "/api/qa")
	if status != http.StatusOK {
		t.Fatalf("GET /api/qa: status %d", status)
	}
	items, _ := listOut["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("initial Q&A list: got %d items, want 0", len(items))
	}

	created, status := send(t, ts, http.MethodPost, "/api/qa", map[string]any{
		"question": "Tell me about yourself.",
	})
	if status != http.StatusOK {
		t.Fatalf("POST /api/qa: status %d, body: %v", status, created)
	}
	if created["question"] != "Tell me about yourself." {
		t.Errorf("created question = %v", created["question"])
	}
	if created["answer"] != "" {
		t.Errorf("created answer without body field: got %v, want empty", created["answer"])
	}
	id := int64(created["id"].(float64))

	withAnswer, status := send(t, ts, http.MethodPost, "/api/qa", map[string]any{
		"question": "What is your notice period?",
		"answer":   "Two weeks.",
	})
	if status != http.StatusOK {
		t.Fatalf("POST /api/qa with answer: status %d, body: %v", status, withAnswer)
	}
	if withAnswer["answer"] != "Two weeks." {
		t.Errorf("created answer = %v, want %q", withAnswer["answer"], "Two weeks.")
	}
	withAnswerID := int64(withAnswer["id"].(float64))
	send(t, ts, http.MethodDelete, "/api/qa", map[string]any{"id": withAnswerID})

	updated, status := send(t, ts, http.MethodPatch, "/api/qa", map[string]any{
		"id":     id,
		"answer": "I am a software engineer.",
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH /api/qa: status %d, body: %v", status, updated)
	}
	if updated["answer"] != "I am a software engineer." {
		t.Errorf("updated answer = %v", updated["answer"])
	}

	listOut, _ = get(t, ts, "/api/qa")
	items, _ = listOut["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("after create: %d items, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if item["answer"] != "I am a software engineer." {
		t.Errorf("listed answer = %v", item["answer"])
	}

	_, status = send(t, ts, http.MethodDelete, "/api/qa", map[string]any{"id": id})
	if status != http.StatusNoContent {
		t.Errorf("DELETE /api/qa: status %d, want 204", status)
	}

	listOut, _ = get(t, ts, "/api/qa")
	items, _ = listOut["items"].([]any)
	if len(items) != 0 {
		t.Errorf("after delete: %d items, want 0", len(items))
	}
}

func TestQAEmptyQuestionIsRejected(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	_, status := send(t, ts, http.MethodPost, "/api/qa", map[string]any{"question": ""})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("empty question: status %d, want 422", status)
	}
}

func TestReferenceListsAreComplete(t *testing.T) {
	d := openTestDB(t)
	ts := newServer(t, d)

	body, status := get(t, ts, "/api/reference")
	if status != http.StatusOK {
		t.Fatalf("GET /api/reference: status %d", status)
	}

	countries, _ := body["countries"].([]any)
	if len(countries) == 0 {
		t.Error("countries list must not be empty")
	}

	regions, _ := body["regions"].([]any)
	if len(regions) == 0 {
		t.Error("regions list must not be empty")
	}

	bands, _ := body["timeZoneBands"].([]any)
	if len(bands) != 6 {
		t.Errorf("timeZoneBands: got %d, want 6", len(bands))
	}

	opts, _ := body["options"].(map[string]any)
	if opts == nil {
		t.Fatal("options missing from reference response")
	}

	for _, r := range regions {
		rm := r.(map[string]any)
		if rm["code"] != "EU" {
			continue
		}
		if size := int(rm["size"].(float64)); size != 27 {
			t.Errorf("EU size = %d, want 27", size)
		}
		members, _ := rm["countries"].([]any)
		if len(members) != 27 {
			t.Errorf("EU countries count = %d, want 27", len(members))
		}
		return
	}
	t.Error("EU region not found in reference data")
}
