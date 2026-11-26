package inbox_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/catalog"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/fit"
	"github.com/sifty-works/sifty-server/internal/listing"
)

const fixtureCount = 1200

func TestListMeasure(t *testing.T) {
	if os.Getenv("SIFTY_MEASURE") != "1" {
		t.Skip("set SIFTY_MEASURE=1 to run")
	}

	database := buildFixtureDB(t)
	ts := newTestServer(t, database)

	get := func(path string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Host = "127.0.0.1:9999"
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
	}

	const runs = 100

	noQTimes := make([]time.Duration, runs)
	for i := range noQTimes {
		start := time.Now()
		get("/api/vacancies")
		noQTimes[i] = time.Since(start)
	}

	textTimes := make([]time.Duration, runs)
	for i := range textTimes {
		start := time.Now()
		get("/api/vacancies?q=xenophon2718")
		textTimes[i] = time.Since(start)
	}

	fitTimes := make([]time.Duration, runs)
	for i := range fitTimes {
		start := time.Now()
		get("/api/vacancies?sort=fit&fit=partial,weak")
		fitTimes[i] = time.Since(start)
	}

	fmt.Printf("list p95 no-query:   %v\n", percentile95(noQTimes))
	fmt.Printf("list p95 text-query: %v\n", percentile95(textTimes))
	fmt.Printf("list p95 fit-filter: %v\n", percentile95(fitTimes))
}

func buildFixtureDB(t *testing.T) *db.DB {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "fixture.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	nowFn := func() time.Time { return time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC) }
	cat := catalog.New(database, nowFn)

	for i := 0; i < fixtureCount; i++ {
		body := makeFixtureBody(i)
		p := listing.Posting{
			URL:            fmt.Sprintf("https://jobs.example.com/vacancy/%d", i),
			Title:          fmt.Sprintf("Software Engineer Level %d", (i%5)+1),
			Company:        fmt.Sprintf("Company %d", i%80),
			Location:       "Remote · Worldwide",
			Published:      time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(i % 90)),
			PublishedBasis: "datePosted",
			Text:           body,

			TextState: listing.TextFull,
		}
		if _, _, err := cat.Upsert(context.Background(), p, "measure"); err != nil {
			t.Fatalf("upsert vacancy %d: %v", i, err)
		}
	}
	var vacIDs []int64
	idRows, err := database.QueryContext(context.Background(), `SELECT id FROM vacancy ORDER BY id LIMIT 50`)
	if err != nil {
		t.Fatalf("query vacancy ids: %v", err)
	}
	for idRows.Next() {
		var id int64
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			t.Fatalf("scan vacancy id: %v", err)
		}
		vacIDs = append(vacIDs, id)
	}
	idRows.Close()
	for _, id := range vacIDs {
		database.Exec(`INSERT INTO job (kind, subject, state, priority, not_before, created, updated)
			VALUES ('analyze_vacancy', ?, 'queued', 0, '', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`,
			fmt.Sprintf("%d/1", id))
	}

	database.Exec(`UPDATE profile SET data='{"name":"Profile","keywords":["engineer"],"targetRoles":["Software Engineer"]}' WHERE id=1`)
	if err := fit.Recompute(context.Background(), database, defaultRules, func(int, int, string) {}); err != nil {
		t.Fatalf("recompute fit: %v", err)
	}
	return database
}

func makeFixtureBody(index int) string {
	var b strings.Builder
	b.WriteString("<h2>About the role</h2>")
	b.WriteString("<p>We are looking for a talented engineer to join our growing team.</p>")
	b.WriteString("<h3>Requirements</h3><ul>")
	skills := []string{
		"Go", "Python", "TypeScript", "Rust", "Java",
		"PostgreSQL", "Redis", "Kafka", "Kubernetes", "Terraform",
	}
	for _, s := range skills {
		fmt.Fprintf(&b, "<li>Experience with %s</li>", s)
	}
	b.WriteString("</ul>")
	b.WriteString("<h3>What we offer</h3><ul>")
	for j := 0; j < 10; j++ {
		fmt.Fprintf(&b, "<li>Competitive benefit number %d in our package</li>", j)
	}
	b.WriteString("</ul>")
	para := "<p>We build software that helps teams collaborate more effectively across time zones and geographies. Our platform serves millions of users worldwide and processes billions of events each day, requiring engineers who understand both the craft of writing clean code and the operational realities of running distributed systems at scale.</p>"
	for b.Len() < 4800 {
		b.WriteString(para)
	}
	if index == 500 {
		b.WriteString("<p>Contact our recruiter xenophon2718 for more details.</p>")
	}
	return b.String()
}

func TestVacancyViewMeasure(t *testing.T) {
	if os.Getenv("SIFTY_MEASURE") != "1" {
		t.Skip("set SIFTY_MEASURE=1 to run")
	}
	database := buildFixtureDB(t)
	var firstID int64
	database.QueryRow(`SELECT id FROM vacancy ORDER BY id LIMIT 1`).Scan(&firstID)
	ts := newTestServer(t, database)
	const runs = 100
	times := make([]time.Duration, runs)
	for i := range times {
		start := time.Now()
		req, _ := http.NewRequest(http.MethodGet,
			fmt.Sprintf("%s/api/vacancies/%d?profile=1", ts.URL, firstID), nil)
		req.Host = "127.0.0.1:9999"
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		resp.Body.Close()
		times[i] = time.Since(start)
	}
	fmt.Printf("vacancy view p95: %v\n", percentile95(times))
}

func percentile95(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(d))
	copy(cp, d)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(float64(len(cp)) * 0.95)
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}
