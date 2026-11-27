package inbox_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
)

func reqText(r any) string {
	m, _ := r.(map[string]any)
	f, _ := m["fit"].(map[string]any)
	v, _ := f["requirementsText"].(string)
	return v
}

func bandReqText(body map[string]any) string {
	b, _ := body["band"].(map[string]any)
	v, _ := b["requirementsText"].(string)
	return v
}

func queueAnalysis(t *testing.T, d *db.DB, id int64) {
	t.Helper()
	_, err := d.Exec(`INSERT INTO job (kind,subject,state,priority,not_before,created,updated)
		VALUES ('analyze_vacancy',?,'queued',0,'','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z')`,
		fmt.Sprintf("%d/1", id))
	if err != nil {
		t.Fatalf("queueReading: %v", err)
	}
}

func TestRequirementsCounter_RowText(t *testing.T) {
	for i, tc := range []struct {
		name, fitRow string
		near         int
		queued       bool
		want         string
	}{
		{name: "without a full job description", fitRow: "0,'no_text',NULL,NULL,NULL,NULL",
			want: "Full job description is unavailable"},
		{name: "before any analysis", want: "Not analysed yet"},
		{name: "while the analysis is queued", queued: true, want: "Analysing requirements…"},
		{name: "with no requirements stated", fitRow: "0,'scored',75,0,0,0", want: "No requirements stated"},
		{name: "with every requirement unchecked", fitRow: "0,'scored',75,0,0,4", want: "Not checked yet"},
		{name: "with checked requirements", fitRow: "0,'scored',75,3,1,2", want: "✓ 3 · ✕ 1 · ? 2"},
		{name: "with a borderline requirement", fitRow: "0,'scored',75,3,1,2", near: 1, want: "✓ 3 · ≈ 1 · ✕ 1 · ? 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			setProfileData(t, d, nil, []string{"description"}, nil)
			id := add(t, newCat(d), post(fmt.Sprintf("https://example.com/jobs/ct%d", i), "Role", "Co"))
			if tc.fitRow != "" {
				p, _ := fit.LoadProfile(context.Background(), d, 1, defaultRules())
				d.Exec(fmt.Sprintf(`INSERT INTO fit (profile,vacancy,version,inputs,listing,state,score,req_ok,req_no,req_maybe)
					VALUES (1,%d,1,%d,%s)`, id, p.Inputs, tc.fitRow))
				if tc.near > 0 {
					d.Exec(`UPDATE fit SET req_near=? WHERE vacancy=?`, tc.near, id)
				}
			}
			if tc.queued {
				queueAnalysis(t, d, id)
			}
			rows := getRows(apiGet(t, newTestServer(t, d), "/api/vacancies"))
			if len(rows) != 1 {
				t.Fatalf("want 1 row, got %d", len(rows))
			}
			if got := reqText(rows[0]); got != tc.want {
				t.Errorf("counter: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequirementsCounter_BandText(t *testing.T) {
	for i, tc := range []struct {
		name   string
		queued bool
		want   string
	}{
		{name: "before any analysis", want: "Not analysed yet"},
		{name: "while the analysis is queued", queued: true, want: "Analysing requirements…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			id := add(t, newCat(d), post(fmt.Sprintf("https://example.com/jobs/cb%d", i), "Role", "Co"))
			if tc.queued {
				queueAnalysis(t, d, id)
			}
			body, _ := apiGetRaw(t, newTestServer(t, d), fmt.Sprintf("/api/vacancies/%d?profile=1", id))
			if got := bandReqText(body); got != tc.want {
				t.Errorf("band counter: got %q, want %q", got, tc.want)
			}
			band, _ := body["band"].(map[string]any)
			if groups, ok := band["groups"].([]any); !ok || len(groups) != 0 {
				t.Errorf("band groups without a reading: want an empty list, got %v", band["groups"])
			}
		})
	}
}
