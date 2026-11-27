package inbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/facts"
	"github.com/sifty-works/sifty-server/internal/fit"
)

func newSkillsServer(t *testing.T, d *db.DB, changed func()) *httptest.Server {
	t.Helper()
	return newServer(t, d, serverOpts{rules: defaultRules, changed: changed})
}

func addVacancyWithRequirements(t *testing.T, d *db.DB, url, title string, reqs []fit.Requirement) int64 {
	t.Helper()
	id := add(t, newCat(d), postWithText(url, title, "Acme", "job text"))
	found := []facts.Fact{{Kind: facts.Analysed, Value: "test", Source: facts.Source{Way: facts.Vocabulary}}}
	for _, r := range reqs {
		found = append(found, facts.Fact{Kind: facts.Requirement, Value: r.Text, Flag: facts.Flag(r.Level), Source: facts.Source{Way: facts.Vocabulary}})
	}
	if err := facts.Save(context.Background(), d, id, 1, "requirement_lines", found); err != nil {
		t.Fatalf("save facts: %v", err)
	}
	return id
}

func postSkills(t *testing.T, ts *httptest.Server, vacancyID int64, body map[string]any) (map[string]any, int) {
	t.Helper()
	payload := map[string]any{"vacancyId": vacancyID}
	for k, v := range body {
		payload[k] = v
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/me/skills", bytes.NewReader(b))
	req.Host = "127.0.0.1:9999"
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /api/me/skills: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode
}

func level(skill, level string) map[string]any {
	return map[string]any{"skill": skill, "level": level}
}

func row(index int, have bool) map[string]any {
	return map[string]any{"requirementIndex": index, "have": have}
}

func TestSkillsDecision_levelRepeatedChangesNothing(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk5", "DevOps",
		[]fit.Requirement{{Text: "Docker", Level: "required"}})
	ts := newSkillsServer(t, d, nil)

	postSkills(t, ts, id, level("Docker", "Expert"))
	resp, status := postSkills(t, ts, id, level("Docker", "Expert"))
	if status != http.StatusOK {
		t.Fatalf("second answer: status %d", status)
	}
	if notice, _ := resp["notice"].(string); notice != "Nothing new to add; your data is already up to date." {
		t.Errorf("second answer notice: %q", notice)
	}
}

func TestSkillsDecision_unknownRequirementConfirmationIsSaved(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk10", "Role",
		[]fit.Requirement{{Text: "Strong communication skills", Level: "required"}})
	ts := newSkillsServer(t, d, nil)

	postSkills(t, ts, id, row(0, true))

	me, err := candidate.LoadMe(context.Background(), d)
	if err != nil {
		t.Fatalf("LoadMe: %v", err)
	}
	found := false
	for _, f := range me.ConfirmedFacts {
		if f.Text == "Strong communication skills" && f.Have {
			found = true
		}
	}
	if !found {
		t.Errorf("confirmed facts %v — want 'Strong communication skills' with Have true", me.ConfirmedFacts)
	}
}

func TestSkillsDecision_invalidInputRefused(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk6", "X", nil)
	ts := newSkillsServer(t, d, nil)
	if _, s := postSkills(t, ts, id, row(5, true)); s != http.StatusUnprocessableEntity {
		t.Errorf("bad index: got %d, want 422", s)
	}
	if _, s := postSkills(t, ts, 999999, row(0, true)); s != http.StatusNotFound {
		t.Errorf("missing vacancy: got %d, want 404", s)
	}
}

func TestSkillsDecision_recomputeFitQueued(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk7", "Infra",
		[]fit.Requirement{{Text: "Kubernetes", Level: "required"}})
	queued := 0
	ts := newSkillsServer(t, d, func() { queued++ })

	postSkills(t, ts, id, level("Kubernetes", "Beginner"))
	if queued != 1 {
		t.Errorf("recompute_fit queued: %d times, want 1", queued)
	}
}

func TestSkillsDecision_requirementGroupsInBand(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk8", "Backend", []fit.Requirement{
		{Text: "Go", Level: "required"},
		{Text: "Docker", Level: "preferred"},
		{Text: "Strong communication skills", Level: "required"},
	})
	ts := newSkillsServer(t, d, nil)

	resp, status := apiGetRaw(t, ts, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
	if status != http.StatusOK {
		t.Fatalf("GET vacancy: status %d", status)
	}
	band, _ := resp["band"].(map[string]any)
	groups, _ := band["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("band.groups = %v, want Required and Nice to have", groups)
	}
	required, _ := groups[0].(map[string]any)
	if skills, _ := required["skills"].([]any); len(skills) != 1 {
		t.Errorf("Required skills = %v, want Go alone", skills)
	}
	if items, _ := required["items"].([]any); len(items) != 1 {
		t.Errorf("Required rows = %v, want the line that names no skill", items)
	}
}

func seedMe(t *testing.T, d *db.DB, skills []candidate.Skill, absent []candidate.AbsentSkill) {
	t.Helper()
	me, err := candidate.LoadMe(context.Background(), d)
	if err != nil {
		t.Fatalf("LoadMe: %v", err)
	}
	me.Skills = skills
	me.AbsentSkills = absent
	if err := candidate.SaveMe(context.Background(), d, me); err != nil {
		t.Fatalf("SaveMe: %v", err)
	}
}

func skillNames(me candidate.MeData) (have, absent []string) {
	for _, s := range me.Skills {
		have = append(have, s.Name+":"+s.Level)
	}
	for _, a := range me.AbsentSkills {
		absent = append(absent, a.Name)
	}
	return
}

func TestSkillsDecision_levelWritesMyData(t *testing.T) {
	for _, tc := range []struct {
		name       string
		text       string
		skills     []candidate.Skill
		absent     []candidate.AbsentSkill
		answer     map[string]any
		notice     string
		wantHave   []string
		wantAbsent []string
	}{
		{name: "a skill not in My data is added at its level", text: "Kafka is a plus",
			answer:   level("Kafka", "Expert"),
			notice:   "Set Kafka to Expert in your skills (change in your profile).",
			wantHave: []string{"Kafka:Expert"}},
		{name: "a skill held under another name is changed in place", text: "Go",
			skills:   []candidate.Skill{{Name: "Golang", Level: "Beginner"}},
			answer:   level("Go", "Expert"),
			notice:   "Set Go to Expert in your skills (change in your profile).",
			wantHave: []string{"Golang:Expert"}},
		{name: "a level moves the skill out of the skills not had", text: "Go",
			absent:   []candidate.AbsentSkill{{Name: "Go"}},
			answer:   level("Go", "Proficient"),
			notice:   "Set Go to Proficient in your skills (change in your profile).",
			wantHave: []string{"Go:Proficient"}},
		{name: "No moves the skill to the skills not had", text: "SQL",
			skills:     []candidate.Skill{{Name: "SQL", Level: "Expert"}},
			answer:     level("SQL", "No"),
			notice:     "Set SQL to skills you do not have (change in your profile).",
			wantAbsent: []string{"SQL"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			id := addVacancyWithRequirements(t, d, "https://example.com/jobs/lv-"+tc.name, "Role",
				[]fit.Requirement{{Text: tc.text, Level: "required"}})
			seedMe(t, d, tc.skills, tc.absent)
			ts := newSkillsServer(t, d, nil)

			resp, status := postSkills(t, ts, id, tc.answer)
			if status != http.StatusOK {
				t.Fatalf("status %d: %v", status, resp)
			}
			if got, _ := resp["notice"].(string); got != tc.notice {
				t.Errorf("notice: got %q, want %q", got, tc.notice)
			}
			me, _ := candidate.LoadMe(context.Background(), d)
			have, absent := skillNames(me)
			if fmt.Sprint(have) != fmt.Sprint(tc.wantHave) || fmt.Sprint(absent) != fmt.Sprint(tc.wantAbsent) {
				t.Errorf("skills %v, absent %v; want %v, %v", have, absent, tc.wantHave, tc.wantAbsent)
			}
		})
	}
}
