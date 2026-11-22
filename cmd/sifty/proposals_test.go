package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/models"
)

type proposalFix struct {
	analyzeFix
}

func newProposalFix(t *testing.T) proposalFix {
	t.Helper()
	fix := newAnalyzeFix(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go fix.r.Start(ctx)
	return proposalFix{fix}
}

func (f proposalFix) analyse(t *testing.T, docID float64, facts ...map[string]any) {
	t.Helper()
	f.fm.AddReply(models.Response{Text: fakeProposalReply(facts)})
	_, status := apiSend(t, http.MethodPost, f.base, fmt.Sprintf("/api/documents/%.0f/analyze", docID), nil)
	if status != http.StatusOK {
		t.Fatalf("analyze: status %d", status)
	}
	waitForDocState(t, f.base, docID, "analysed", 5*time.Second)
}

func (f proposalFix) addDocument(t *testing.T, text string) float64 {
	t.Helper()
	out, status := pasteAnalyzeDoc(t, f.base, "cv.txt", text)
	if status != http.StatusOK {
		t.Fatalf("paste document: status %d", status)
	}
	return out["id"].(float64)
}

func (f proposalFix) proposals(t *testing.T) []map[string]any {
	t.Helper()
	out, status := apiGet(t, f.base, "/api/proposals")
	if status != http.StatusOK {
		t.Fatalf("GET /api/proposals: status %d", status)
	}
	return proposalMaps(out)
}

func proposalMaps(out map[string]any) []map[string]any {
	var list []map[string]any
	items, _ := out["proposals"].([]any)
	for _, it := range items {
		list = append(list, it.(map[string]any))
	}
	return list
}

func (f proposalFix) idOf(t *testing.T, value string) float64 {
	t.Helper()
	for _, p := range f.proposals(t) {
		if p["value"] == value {
			return p["id"].(float64)
		}
	}
	t.Fatalf("no proposal %q in the list", value)
	return 0
}

func (f proposalFix) answer(t *testing.T, verb string, id float64) (map[string]any, int) {
	t.Helper()
	return apiSend(t, http.MethodPost, f.base, fmt.Sprintf("/api/proposals/%.0f/%s", id, verb), nil)
}

func (f proposalFix) me(t *testing.T) map[string]any {
	t.Helper()
	out, status := apiGet(t, f.base, "/api/me")
	if status != http.StatusOK {
		t.Fatalf("GET /api/me: status %d", status)
	}
	return out
}

func (f proposalFix) profiles(t *testing.T) (first, active map[string]any) {
	t.Helper()
	list, _ := apiGet(t, f.base, "/api/profiles")
	for i, p := range list["profiles"].([]any) {
		pm := p.(map[string]any)
		detail, _ := apiGet(t, f.base, fmt.Sprintf("/api/profiles/%.0f", pm["id"].(float64)))
		own := detail["own"].(map[string]any)
		if i == 0 {
			first = own
		}
		if pm["active"] == true {
			active = own
		}
	}
	return first, active
}

func listAt(m map[string]any, key string) []any {
	l, _ := m[key].([]any)
	return l
}

func TestAcceptedFactLandsWhereItBelongs(t *testing.T) {
	cases := []struct {
		name, text  string
		fact        map[string]any
		profileRole bool
		check       func(t *testing.T, me map[string]any)
	}{
		{
			name: "skill goes to My data at Proficient from a document", text: "Experienced in Go.",
			fact: map[string]any{"field": "skill", "value": "Go (Expert)", "quote": "Experienced in Go."},
			check: func(t *testing.T, me map[string]any) {
				skills := listAt(me, "skills")
				if len(skills) != 1 || fmt.Sprint(skills[0]) != "map[level:Proficient name:Go source:document]" {
					t.Errorf("skills = %v, want Go at Proficient from a document", skills)
				}
			},
		},
		{
			name: "language goes to My data with its level", text: "French at B2.",
			fact: map[string]any{"field": "language", "value": "French (B2)", "quote": "French at B2."},
			check: func(t *testing.T, me map[string]any) {
				langs := listAt(me, "languages")
				if len(langs) != 1 || fmt.Sprint(langs[0]) != "map[level:B2 name:French]" {
					t.Errorf("languages = %v, want French at B2", langs)
				}
			},
		},
		{
			name: "experience goes to My data with its years", text: "Lead Engineer at Nexus, 2019-2022.",
			fact: map[string]any{"field": "experience", "value": "Lead Engineer at Nexus, 2019-2022", "quote": "Lead Engineer at Nexus, 2019-2022."},
			check: func(t *testing.T, me map[string]any) {
				exp := listAt(me, "experience")
				if len(exp) != 1 || fmt.Sprint(exp[0]) != "map[company:Nexus end:2022 start:2019 title:Lead Engineer]" {
					t.Errorf("experience = %v, want Lead Engineer at Nexus 2019 to 2022", exp)
				}
			},
		},
		{
			name: "target role goes to the active profile, not My data", text: "Seeking a Backend Engineer role.", profileRole: true,
			fact: map[string]any{"field": "targetRole", "value": "Backend Engineer", "quote": "Seeking a Backend Engineer role."},
			check: func(t *testing.T, me map[string]any) {
				for _, k := range []string{"skills", "languages", "experience"} {
					if len(listAt(me, k)) != 0 {
						t.Errorf("My data %s = %v, want none", k, listAt(me, k))
					}
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newProposalFix(t)
			if _, status := apiSend(t, http.MethodPost, fix.base, "/api/profiles", map[string]any{"name": "Second"}); status != http.StatusOK {
				t.Fatalf("create profile: status %d", status)
			}
			fix.analyse(t, fix.addDocument(t, tc.text), tc.fact)

			out, status := fix.answer(t, "accept", fix.idOf(t, tc.fact["value"].(string)))
			if status != http.StatusOK || len(proposalMaps(out)) != 0 {
				t.Fatalf("accept: status %d, remaining %v, want 200 and none", status, out)
			}

			tc.check(t, fix.me(t))
			first, active := fix.profiles(t)
			wantRoles := 0
			if tc.profileRole {
				wantRoles = 1
			}
			if got := listAt(active, "targetRoles"); len(got) != wantRoles {
				t.Errorf("active profile targetRoles = %v, want %d", got, wantRoles)
			}
			if got := listAt(first, "targetRoles"); len(got) != 0 {
				t.Errorf("first profile targetRoles = %v, want none", got)
			}
		})
	}
}

func TestAcceptedSkillIsNotOfferedAgainAndNotDuplicatedWhenAlreadyHeld(t *testing.T) {
	fix := newProposalFix(t)
	doc := fix.addDocument(t, "Experienced in Go. Familiar with Python.")
	goFact := map[string]any{"field": "skill", "value": "Go", "quote": "Experienced in Go."}
	pyFact := map[string]any{"field": "skill", "value": "Python", "quote": "Familiar with Python."}
	fix.analyse(t, doc, goFact, pyFact)

	if _, status := fix.answer(t, "accept", fix.idOf(t, "Go")); status != http.StatusOK {
		t.Fatalf("accept: status %d", status)
	}
	fix.analyse(t, doc, goFact, pyFact)
	if got := fix.proposals(t); len(got) != 1 || got[0]["value"] != "Python" {
		t.Errorf("after analysing again: %v, want only Python", got)
	}

	if _, status := apiSend(t, http.MethodPatch, fix.base, "/api/me", map[string]any{"skills": []map[string]any{}}); status != http.StatusOK {
		t.Fatalf("remove skill: status %d", status)
	}
	fix.analyse(t, doc, goFact, pyFact)
	if got := fix.proposals(t); len(got) != 1 || got[0]["value"] != "Python" {
		t.Errorf("after the accepted skill was removed and the document analysed again: %v, want only Python", got)
	}

	pythonID := fix.idOf(t, "Python")
	if _, status := apiSend(t, http.MethodPatch, fix.base, "/api/me", map[string]any{"skills": []map[string]any{{"name": "python", "level": "Beginner"}}}); status != http.StatusOK {
		t.Fatalf("add skill by hand: status %d", status)
	}
	if _, status := fix.answer(t, "accept", pythonID); status != http.StatusOK {
		t.Fatalf("accept a skill already added by hand: status %d", status)
	}
	if skills := listAt(fix.me(t), "skills"); len(skills) != 1 {
		t.Errorf("skills = %v, want the one added by hand", skills)
	}
}

func TestSecondRoleAtAHeldCompanyIsOffered(t *testing.T) {
	fix := newProposalFix(t)
	if _, status := apiSend(t, http.MethodPatch, fix.base, "/api/me", map[string]any{"experience": []map[string]any{{"company": "Nexus", "title": "Engineer"}}}); status != http.StatusOK {
		t.Fatalf("patch experience: status %d", status)
	}
	doc := fix.addDocument(t, "Engineer at Nexus. Lead Engineer at Nexus.")
	fix.analyse(t, doc,
		map[string]any{"field": "experience", "value": "Engineer at Nexus", "quote": "Engineer at Nexus."},
		map[string]any{"field": "experience", "value": "Lead Engineer at Nexus", "quote": "Lead Engineer at Nexus."})
	if got := fix.proposals(t); len(got) != 1 || got[0]["value"] != "Lead Engineer at Nexus" {
		t.Errorf("proposals = %v, want only the Lead Engineer role", got)
	}
}

func TestRejectingOneRoleAtACompanyDoesNotHideAnother(t *testing.T) {
	fix := newProposalFix(t)
	doc := fix.addDocument(t, "Engineer at Nexus. Lead Engineer at Nexus.")
	facts := []map[string]any{
		{"field": "experience", "value": "Engineer at Nexus", "quote": "Engineer at Nexus."},
		{"field": "experience", "value": "Lead Engineer at Nexus", "quote": "Lead Engineer at Nexus."},
	}
	fix.analyse(t, doc, facts...)
	fix.answer(t, "reject", fix.idOf(t, "Engineer at Nexus"))
	fix.analyse(t, doc, facts...)
	if got := fix.proposals(t); len(got) != 1 || got[0]["value"] != "Lead Engineer at Nexus" {
		t.Errorf("proposals = %v, want only the Lead Engineer role", got)
	}
}

func TestRejectedProposalLeavesListWritesNothingAndIsNotOfferedAgain(t *testing.T) {
	fix := newProposalFix(t)
	doc := fix.addDocument(t, "Experienced in Go. Familiar with Python.")
	goFact := map[string]any{"field": "skill", "value": "Go", "quote": "Experienced in Go."}
	pyFact := map[string]any{"field": "skill", "value": "Python", "quote": "Familiar with Python."}
	fix.analyse(t, doc, goFact, pyFact)
	meBefore := fix.me(t)
	_, profileBefore := fix.profiles(t)

	out, status := fix.answer(t, "reject", fix.idOf(t, "Go"))
	if status != http.StatusOK {
		t.Fatalf("reject: status %d body %v", status, out)
	}
	if left := proposalMaps(out); len(left) != 1 || left[0]["value"] != "Python" {
		t.Errorf("list after reject = %v, want only Python", left)
	}
	_, profileAfter := fix.profiles(t)
	if fmt.Sprint(meBefore) != fmt.Sprint(fix.me(t)) || fmt.Sprint(profileBefore) != fmt.Sprint(profileAfter) {
		t.Error("reject wrote to My data or the profile")
	}

	fix.analyse(t, doc, goFact, pyFact)
	again := fix.proposals(t)
	if len(again) != 1 || again[0]["value"] != "Python" {
		t.Errorf("after analysing again: %v, want only Python", again)
	}
	docs, _ := apiGet(t, fix.base, "/api/documents")
	if reason := docs["documents"].([]any)[0].(map[string]any)["stateReason"]; reason != "1" {
		t.Errorf("document facts after analysing again = %v, want 1 (the Go already rejected is not counted)", reason)
	}
}

func TestAnsweringAProposalTwiceOrAnUnknownOneIsRefused(t *testing.T) {
	fix := newProposalFix(t)
	doc := fix.addDocument(t, "Experienced in Go.")
	fix.analyse(t, doc, map[string]any{"field": "skill", "value": "Go", "quote": "Experienced in Go."})
	id := fix.idOf(t, "Go")
	if _, status := fix.answer(t, "accept", id); status != http.StatusOK {
		t.Fatalf("first accept: status %d", status)
	}
	for _, verb := range []string{"accept", "reject"} {
		if _, status := fix.answer(t, verb, id); status != http.StatusConflict {
			t.Errorf("%s again: status %d, want 409", verb, status)
		}
	}
	if _, status := fix.answer(t, "accept", 9999); status != http.StatusNotFound {
		t.Errorf("accept unknown: status %d, want 404", status)
	}
	if skills := fix.me(t)["skills"].([]any); len(skills) != 1 {
		t.Errorf("skills = %v, want one", skills)
	}
}

func (f proposalFix) setLanguages(t *testing.T, langs ...map[string]any) {
	t.Helper()
	if _, status := apiSend(t, http.MethodPatch, f.base, "/api/me", map[string]any{"languages": langs}); status != http.StatusOK {
		t.Fatalf("patch languages: status %d", status)
	}
}

func languagesOf(t *testing.T, f proposalFix) []string {
	t.Helper()
	var out []string
	langs, _ := f.me(t)["languages"].([]any)
	for _, l := range langs {
		lm := l.(map[string]any)
		out = append(out, fmt.Sprintf("%v/%v", lm["name"], lm["level"]))
	}
	return out
}

func TestLanguageAtAnotherLevelIsAConflictCarryingBothValues(t *testing.T) {
	fix := newProposalFix(t)
	fix.setLanguages(t, map[string]any{"name": "French", "level": "B1"})
	doc := fix.addDocument(t, "French at B2.")
	fact := map[string]any{"field": "language", "value": "French (B2)", "quote": "French at B2."}
	fix.analyse(t, doc, fact)

	list := fix.proposals(t)
	if len(list) != 1 || list[0]["value"] != "French (B2)" || list[0]["current"] != "French (B1)" {
		t.Fatalf("list = %v, want one conflict with value French (B2) and current French (B1)", list)
	}

	fix.answer(t, "reject", list[0]["id"].(float64))
	if got := languagesOf(t, fix); len(got) != 1 || got[0] != "French/B1" {
		t.Errorf("after keeping the current value: languages = %v, want French/B1", got)
	}
	fix.analyse(t, doc, fact)
	if got := fix.proposals(t); len(got) != 0 {
		t.Errorf("a conflict already settled came back: %v", got)
	}
}

func TestUsingTheProposedLanguageLevelReplacesTheCurrentOne(t *testing.T) {
	fix := newProposalFix(t)
	fix.setLanguages(t, map[string]any{"name": "French", "level": "B1"})
	doc := fix.addDocument(t, "French at B2.")
	fix.analyse(t, doc, map[string]any{"field": "language", "value": "French (B2)", "quote": "French at B2."})

	if _, status := fix.answer(t, "accept", fix.idOf(t, "French (B2)")); status != http.StatusOK {
		t.Fatalf("accept: status %d", status)
	}
	if got := languagesOf(t, fix); len(got) != 1 || got[0] != "French/B2" {
		t.Errorf("languages = %v, want French/B2 only", got)
	}
}

func TestLanguageWithoutALevelIsFilledNotConflicted(t *testing.T) {
	fix := newProposalFix(t)
	fix.setLanguages(t, map[string]any{"name": "French"})
	doc := fix.addDocument(t, "French at B2.")
	fix.analyse(t, doc, map[string]any{"field": "language", "value": "French (B2)", "quote": "French at B2."})

	list := fix.proposals(t)
	if len(list) != 1 || list[0]["current"] != nil {
		t.Fatalf("list = %v, want one proposal without a current value", list)
	}
	fix.answer(t, "accept", list[0]["id"].(float64))
	if got := languagesOf(t, fix); len(got) != 1 || got[0] != "French/B2" {
		t.Errorf("languages = %v, want French/B2", got)
	}
}

func TestSkillAtAnotherLevelIsNotOffered(t *testing.T) {
	fix := newProposalFix(t)
	if _, status := apiSend(t, http.MethodPatch, fix.base, "/api/me", map[string]any{"skills": []map[string]any{{"name": "Go", "level": "Beginner"}}}); status != http.StatusOK {
		t.Fatalf("patch skills: status %d", status)
	}
	doc := fix.addDocument(t, "Expert in Go.")
	fix.analyse(t, doc, map[string]any{"field": "skill", "value": "Go (Expert)", "quote": "Expert in Go."})
	if got := fix.proposals(t); len(got) != 0 {
		t.Errorf("proposals = %v, want none for a skill already held", got)
	}
}

func recomputeJobs(t *testing.T, fix analyzeFix) (total, queued int) {
	t.Helper()
	fix.d.QueryRow(`SELECT count(*), COALESCE(SUM(state='queued'),0) FROM job WHERE kind='recompute_fit'`).Scan(&total, &queued)
	return
}

func TestAcceptQueuesRecomputationAndAnswersWithoutWaitingForIt(t *testing.T) {
	fix := newAnalyzeFix(t)
	doc, _ := pasteAnalyzeDoc(t, fix.base, "cv.txt", "Experienced in Go.")
	if _, err := fix.d.Exec(`INSERT INTO proposal (document_id, field, value, quote, to_review, created_at) VALUES (?, 'skill', 'Go', 'Experienced in Go.', 1, '2026-01-01T00:00:00Z')`, int64(doc["id"].(float64))); err != nil {
		t.Fatal(err)
	}
	var id int64
	fix.d.QueryRow(`SELECT id FROM proposal`).Scan(&id)
	if total, _ := recomputeJobs(t, fix); total != 0 {
		t.Fatalf("recompute jobs before accept = %d, want 0", total)
	}

	if _, status := apiSend(t, http.MethodPost, fix.base, fmt.Sprintf("/api/proposals/%d/accept", id), nil); status != http.StatusOK {
		t.Fatalf("accept: status %d", status)
	}
	if total, queued := recomputeJobs(t, fix); total != 1 || queued != 1 {
		t.Errorf("recompute jobs after accept = %d (%d queued), want one still queued", total, queued)
	}
}
