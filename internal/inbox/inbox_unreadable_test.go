package inbox_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestUnreadableDataRefusesDecisionAndDeletionAndFailsList(t *testing.T) {
	for name, corrupt := range map[string]string{
		"invalid profile JSON": `UPDATE profile SET data='{"targetRoles": 17' WHERE id=1`,
		"invalid My data":      `UPDATE me SET data='{"skills": 17' WHERE id=1`,
	} {
		t.Run(name, func(t *testing.T) {
			d := openTestDB(t)
			setProfileData(t, d, []string{"engineer"}, nil, nil)
			cat := newCat(d)
			open := add(t, cat, post("https://example.com/jobs/up1", "Engineer", "Acme"))
			decided := add(t, cat, post("https://example.com/jobs/up2", "Engineer Two", "Acme"))
			decide(t, d, decided, "shortlist", "")
			ts := newTestServer(t, d)
			if _, err := d.Exec(corrupt); err != nil {
				t.Fatalf("corrupt data: %v", err)
			}

			_, status := apiJSON(t, ts, http.MethodPost, fmt.Sprintf("/api/vacancies/%d/decision", open),
				map[string]any{"profile": 1, "from": "inbox", "to": "shortlist"})
			if status != http.StatusInternalServerError {
				t.Errorf("decision: status %d, want 500", status)
			}
			var written int
			d.QueryRow(`SELECT count(*) FROM decision WHERE profile=1 AND vacancy=?`, open).Scan(&written)
			if written != 0 {
				t.Errorf("the refused decision was written: %d rows", written)
			}

			_, status = apiJSON(t, ts, http.MethodDelete, fmt.Sprintf("/api/vacancies/%d?profile=1", decided), nil)
			if status != http.StatusInternalServerError {
				t.Errorf("deletion: status %d, want 500", status)
			}
			var vacancies, tombstones int
			d.QueryRow(`SELECT count(*) FROM vacancy WHERE id=?`, decided).Scan(&vacancies)
			d.QueryRow(`SELECT count(*) FROM tombstone`).Scan(&tombstones)
			if vacancies != 1 || tombstones != 0 {
				t.Errorf("the refused deletion changed data: %d vacancies, %d tombstones", vacancies, tombstones)
			}

			if _, status := apiGetRaw(t, ts, "/api/vacancies?profile=1"); status != http.StatusInternalServerError {
				t.Errorf("list: status %d, want 500", status)
			}
		})
	}
}
