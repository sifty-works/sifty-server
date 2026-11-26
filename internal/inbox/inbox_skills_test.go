package inbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func postSkills(t *testing.T, ts *httptest.Server, vacancyID int64, reqIndex int, have bool, chosen string) (map[string]any, int) {
	t.Helper()
	payload := map[string]any{
		"vacancyId":        vacancyID,
		"requirementIndex": reqIndex,
		"have":             have,
	}
	if chosen != "" {
		payload["chosenSkill"] = chosen
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

func reqLine(resp map[string]any, idx int) map[string]any {
	vv, _ := resp["vacancyView"].(map[string]any)
	band, _ := vv["band"].(map[string]any)
	groups, _ := band["groups"].([]any)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		items, _ := gm["items"].([]any)
		for _, it := range items {
			im, _ := it.(map[string]any)
			if int(im["index"].(float64)) == idx {
				return im
			}
		}
	}
	return nil
}

func reqNamedSkills(resp map[string]any, idx int) string {
	names, _ := reqLine(resp, idx)["namedSkills"].([]any)
	return fmt.Sprint(names)
}

func reqVerdict(resp map[string]any, idx int) string {
	v, _ := reqLine(resp, idx)["verdict"].(string)
	return v
}

func reqCounter(resp map[string]any) string {
	vv, _ := resp["vacancyView"].(map[string]any)
	band, _ := vv["band"].(map[string]any)
	v, _ := band["requirementsText"].(string)
	return v
}

func TestSkillsDecision_addsSkillAndReturnsNotice(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk1", "Role",
		[]fit.Requirement{{Text: "Golang expertise", Level: "required"}})
	ts := newSkillsServer(t, d, nil)

	resp, status := postSkills(t, ts, id, 0, true, "")
	if status != http.StatusOK {
		t.Fatalf("POST /api/me/skills: status %d, body %v", status, resp)
	}
	notice, _ := resp["notice"].(string)
	if notice == "" || notice == "Nothing new to add; your data is already up to date." {
		t.Errorf("notice: %q — expected something added", notice)
	}
}

func TestSkillsDecision_everyNamedSkillStaysAvailable(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk9", "Platform",
		[]fit.Requirement{{Text: "Docker and Kubernetes in production", Level: "required"}})
	ts := newSkillsServer(t, d, nil)

	first, _ := postSkills(t, ts, id, 0, true, "Docker")
	if got := reqNamedSkills(first, 0); got != "[Docker Kubernetes]" {
		t.Errorf("named skills after the first choice: got %q, want both", got)
	}
	second, _ := postSkills(t, ts, id, 0, true, "Kubernetes")
	if notice, _ := second["notice"].(string); notice != `Added "Kubernetes" to your skills (level Proficient; change in your profile).` {
		t.Errorf("notice of the second choice: %q", notice)
	}
}

func TestSkillsDecision_secondAddDoesNotDuplicate(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk5", "DevOps",
		[]fit.Requirement{{Text: "Docker", Level: "required"}})
	ts := newSkillsServer(t, d, nil)

	postSkills(t, ts, id, 0, true, "")
	resp, status := postSkills(t, ts, id, 0, true, "")
	if status != http.StatusOK {
		t.Fatalf("second add: status %d", status)
	}
	if notice, _ := resp["notice"].(string); notice != "Nothing new to add; your data is already up to date." {
		t.Errorf("second add notice: %q", notice)
	}
}

func TestSkillsDecision_verdictAndCounterInResponse(t *testing.T) {
	for _, tc := range []struct {
		name        string
		req         fit.Requirement
		chosen      string
		wantVerdict string
		wantCounter string
	}{
		{name: "single-skill plus gives verdict checked and counter",
			req:         fit.Requirement{Text: "Go", Level: "required"},
			wantVerdict: "✓", wantCounter: "✓ 1 · ✕ 0 · ? 0"},
		{name: "chosen skill of compound requirement leaves verdict unchecked",
			req:         fit.Requirement{Text: "Docker and Kubernetes in production", Level: "required"},
			chosen:      "Docker",
			wantVerdict: "?", wantCounter: "Not checked yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			id := addVacancyWithRequirements(t, d, "https://example.com/jobs/vc"+tc.name, "Role",
				[]fit.Requirement{tc.req})
			ts := newSkillsServer(t, d, nil)
			resp, _ := postSkills(t, ts, id, 0, true, tc.chosen)
			if got := reqVerdict(resp, 0); got != tc.wantVerdict {
				t.Errorf("verdict: got %q, want %q", got, tc.wantVerdict)
			}
			if got := reqCounter(resp); got != tc.wantCounter {
				t.Errorf("counter: got %q, want %q", got, tc.wantCounter)
			}
		})
	}
}

func TestSkillsDecision_unknownRequirementConfirmationIsSaved(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk10", "Role",
		[]fit.Requirement{{Text: "Strong communication skills", Level: "required"}})
	ts := newSkillsServer(t, d, nil)

	postSkills(t, ts, id, 0, true, "")

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
	if _, s := postSkills(t, ts, id, 5, true, ""); s != http.StatusUnprocessableEntity {
		t.Errorf("bad index: got %d, want 422", s)
	}
	if _, s := postSkills(t, ts, 999999, 0, true, ""); s != http.StatusNotFound {
		t.Errorf("missing vacancy: got %d, want 404", s)
	}
}

func TestSkillsDecision_recomputeFitQueued(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk7", "Infra",
		[]fit.Requirement{{Text: "Kubernetes", Level: "required"}})
	queued := 0
	ts := newSkillsServer(t, d, func() { queued++ })

	postSkills(t, ts, id, 0, true, "")
	if queued != 1 {
		t.Errorf("recompute_fit queued: %d times, want 1", queued)
	}
}

func TestSkillsDecision_requirementGroupsInBand(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/sk8", "Backend", []fit.Requirement{
		{Text: "Go", Level: "required"},
		{Text: "Docker", Level: "preferred"},
	})
	ts := newSkillsServer(t, d, nil)

	resp, status := apiGetRaw(t, ts, fmt.Sprintf("/api/vacancies/%d?profile=1", id))
	if status != http.StatusOK {
		t.Fatalf("GET vacancy: status %d", status)
	}
	band, _ := resp["band"].(map[string]any)
	if groups, _ := band["groups"].([]any); len(groups) == 0 {
		t.Error("band.groups should not be empty when requirements are read")
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
		have = append(have, s.Name)
	}
	for _, a := range me.AbsentSkills {
		absent = append(absent, a.Name)
	}
	return
}

func TestSkillsDecision_skillTakenFromOppositeListIsReportedAsMoved(t *testing.T) {
	for _, tc := range []struct {
		name       string
		text       string
		have       bool
		skills     []candidate.Skill
		absent     []candidate.AbsentSkill
		want       string
		wantHave   []string
		wantAbsent []string
	}{
		{name: "plus on a skill the user did not have", text: "Go", have: true,
			absent:   []candidate.AbsentSkill{{Name: "Go"}},
			want:     `Moved "Go" from skills you do not have to your skills (level Proficient; change in your profile).`,
			wantHave: []string{"Go"}},
		{name: "minus on a skill the user had", text: "SQL", have: false,
			skills:     []candidate.Skill{{Name: "SQL", Level: "Expert"}},
			want:       `Moved "SQL" from your skills to skills you do not have (change in your profile).`,
			wantAbsent: []string{"SQL"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			id := addVacancyWithRequirements(t, d, "https://example.com/jobs/mv-"+tc.name, "Role",
				[]fit.Requirement{{Text: tc.text, Level: "required"}})
			seedMe(t, d, tc.skills, tc.absent)
			queued := 0
			ts := newSkillsServer(t, d, func() { queued++ })

			resp, _ := postSkills(t, ts, id, 0, tc.have, "")
			if got, _ := resp["notice"].(string); got != tc.want {
				t.Errorf("notice: got %q, want %q", got, tc.want)
			}
			if queued != 1 {
				t.Errorf("recompute_fit queued: %d times, want 1", queued)
			}
			me, _ := candidate.LoadMe(context.Background(), d)
			have, absent := skillNames(me)
			if fmt.Sprint(have) != fmt.Sprint(tc.wantHave) || fmt.Sprint(absent) != fmt.Sprint(tc.wantAbsent) {
				t.Errorf("skills %v, absent %v; want %v, %v", have, absent, tc.wantHave, tc.wantAbsent)
			}
		})
	}
}

func TestSkillsDecision_minusOnRequirementReportsEverySkillTakenFromSkills(t *testing.T) {
	d := openTestDB(t)
	id := addVacancyWithRequirements(t, d, "https://example.com/jobs/both", "Platform",
		[]fit.Requirement{{Text: "Docker and Kubernetes in production", Level: "required"}})
	ts := newSkillsServer(t, d, nil)
	postSkills(t, ts, id, 0, true, "Docker")
	postSkills(t, ts, id, 0, true, "Kubernetes")

	resp, _ := postSkills(t, ts, id, 0, false, "")
	notice, _ := resp["notice"].(string)
	for _, want := range []string{
		`Moved "Docker" from your skills to skills you do not have (change in your profile).`,
		`Moved "Kubernetes" from your skills to skills you do not have (change in your profile).`,
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q does not contain %q", notice, want)
		}
	}
	me, _ := candidate.LoadMe(context.Background(), d)
	have, absent := skillNames(me)
	if len(have) != 0 || len(absent) != 2 {
		t.Errorf("skills %v, absent %v; want no skills and both absent", have, absent)
	}
}
