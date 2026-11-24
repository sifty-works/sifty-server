package inbox_test

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/sifty-works/sifty-server/internal/db"
)

func windowFixture(t *testing.T, n int) *db.DB {
	t.Helper()
	d := openTestDB(t)
	setProfileData(t, d, nil, []string{"engineer"}, nil)
	cat := newCat(d)
	for i := 1; i <= n; i++ {
		p := post(fmt.Sprintf("https://example.com/jobs/w%03d", i), fmt.Sprintf("Engineer %03d", i), "Co")
		p.Published = testNow.AddDate(0, 0, -i)
		p.PublishedBasis = "datePosted"
		id := addWithSrc(t, cat, p, []string{"alpha", "beta"}[i%2])
		setVacancyIndustries(t, d, id, []string{[]string{"Fintech", "Health", "Retail"}[i%3]})
	}
	return d
}

func orderedIDs(body map[string]any) []float64 {
	var out []float64
	for _, r := range getRows(body) {
		out = append(out, rowID(r))
	}
	return out
}

func TestWindow_NextWindowContinuesTheFirst(t *testing.T) {
	ts := newTestServer(t, windowFixture(t, 25))
	for _, sort := range []string{"newest", ""} {
		whole := orderedIDs(apiGet(t, ts, "/api/vacancies?limit=200&sort="+sort))
		if len(whole) != 25 {
			t.Fatalf("sort %q: whole list %d rows, want 25", sort, len(whole))
		}
		var joined []float64
		for offset := 0; offset < 25; offset += 10 {
			joined = append(joined, orderedIDs(apiGet(t, ts, fmt.Sprintf("/api/vacancies?limit=10&offset=%d&sort=%s", offset, sort)))...)
		}
		if !reflect.DeepEqual(joined, whole) {
			t.Errorf("sort %q: windows joined %v, want %v", sort, joined, whole)
		}
	}
}

func TestWindow_CountsAndMatchingDoNotChangeWithTheWindow(t *testing.T) {
	ts := newTestServer(t, windowFixture(t, 14))
	for _, tc := range []struct {
		filters  string
		matching float64
	}{{"", 14}, {"&industries=Fintech", 4}, {"&q=zzznomatch", 0}} {
		whole := apiGet(t, ts, "/api/vacancies?limit=200"+tc.filters)
		for _, window := range []string{"limit=1", "limit=3&offset=9", "limit=3&offset=40"} {
			got := apiGet(t, ts, "/api/vacancies?"+window+tc.filters)
			for _, key := range []string{"counts", "industryCounts", "sourceCounts", "total", "matching", "empty"} {
				if !reflect.DeepEqual(got[key], whole[key]) {
					t.Errorf("%s%s, %s: %v, want %v", window, tc.filters, key, got[key], whole[key])
				}
			}
			if got["matching"] != tc.matching {
				t.Errorf("%s%s: matching %v, want %v", window, tc.filters, got["matching"], tc.matching)
			}
			if window == "limit=3&offset=40" && len(getRows(got)) != 0 {
				t.Errorf("%s%s: rows past the end, want none", window, tc.filters)
			}
		}
	}
}

func TestWindow_BoundsAreAcceptedOrRefused(t *testing.T) {
	ts := newTestServer(t, windowFixture(t, 3))
	for query, want := range map[string]int{
		"limit=0":   http.StatusUnprocessableEntity,
		"limit=1":   http.StatusOK,
		"limit=200": http.StatusOK,
		"limit=201": http.StatusUnprocessableEntity,
		"limit=abc": http.StatusUnprocessableEntity,
		"offset=-1": http.StatusUnprocessableEntity,
		"offset=0":  http.StatusOK,
	} {
		body, status := apiGetRaw(t, ts, "/api/vacancies?"+query)
		if status != want {
			t.Errorf("%s: status %d, want %d", query, status, want)
		}
		if _, listed := body["rows"]; listed && status != http.StatusOK {
			t.Errorf("%s: rows listed with a refusal", query)
		}
	}
}
