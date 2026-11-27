package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
)

type providerView struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Model string `json:"model"`
	Cost  string `json:"cost"`
	State string `json:"state"`
}

type workView struct {
	Kind     string   `json:"kind"`
	Provider string   `json:"provider"`
	Choices  []string `json:"choices"`
}

type modelsResp struct {
	Providers     []providerView `json:"providers"`
	Work          []workView     `json:"work"`
	AllowPaidBulk bool           `json:"allowPaidBulk"`
	Paused        bool           `json:"paused"`
	DailyLimit    int            `json:"dailyLimit"`
	TodayCalls    int            `json:"todayCalls"`

	PreciseReading bool `json:"preciseReading"`
}

const twoProviderYAML = `# the providers of this data folder
providers:
  localbox: {type: ollama, model: tiny-local, cost: local}
  cloudbox: {type: openai, model: cloud-mini, cost: paid}
work: {reading: localbox, shortlist_check: localbox, preparation: cloudbox}
daily_model_limit: 25
`

func startModelsServe(t *testing.T) (base, dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sifty.yaml"), []byte(twoProviderYAML), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	port, cancel := startServer(t, dir, true)
	t.Cleanup(cancel)
	base = fmt.Sprintf("http://127.0.0.1:%d", port)
	return base, dir
}

func getModels(t *testing.T, base string) modelsResp {
	t.Helper()
	body, status := apiGet(t, base, "/api/models")
	if status != http.StatusOK {
		t.Fatalf("GET /api/models: status %d", status)
	}
	b, _ := json.Marshal(body)
	var out modelsResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	return out
}

func workProvider(m modelsResp, kind string) string {
	for _, w := range m.Work {
		if w.Kind == kind {
			return w.Provider
		}
	}
	return "none"
}

func TestModelsListsBothProvidersTheWorkKindsAndTheSwitches(t *testing.T) {
	base, _ := startModelsServe(t)

	m := getModels(t, base)

	const want = "[{cloudbox openai cloud-mini paid } {localbox ollama tiny-local local }]"
	if fmt.Sprint(m.Providers) != want {
		t.Errorf("providers: %+v, want %s", m.Providers, want)
	}
	for _, w := range m.Work {
		if fmt.Sprint(w.Choices) != "[cloudbox localbox]" {
			t.Errorf("choices of %s: %v", w.Kind, w.Choices)
		}
	}
	if len(m.Work) != 3 || workProvider(m, "reading") != "localbox" ||
		workProvider(m, "shortlist_check") != "localbox" || workProvider(m, "preparation") != "cloudbox" {
		t.Errorf("kinds of work: %+v", m.Work)
	}
	if m.AllowPaidBulk || m.Paused || m.DailyLimit != 25 || m.TodayCalls != 0 {
		t.Errorf("switches: %+v", m)
	}
}

func TestChangingTheProviderOfAKindOfWorkKeepsTheFileComments(t *testing.T) {
	base, dir := startModelsServe(t)

	if result, status := apiSend(t, http.MethodPatch, base, "/api/models", `{"work":{"reading":"cloudbox"}}`); status != http.StatusOK {
		t.Fatalf("patch: status %d, body %v", status, result)
	}
	if got := workProvider(getModels(t, base), "reading"); got != "cloudbox" {
		t.Errorf("reading provider after the change: %q", got)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "sifty.yaml"))
	if !strings.Contains(string(data), "# the providers of this data folder") ||
		!strings.Contains(string(data), "reading: cloudbox") {
		t.Errorf("settings file after the change:\n%s", data)
	}
}

func TestEachSwitchOfTheModelsSectionChangesOnlyWhatItNames(t *testing.T) {
	base, _ := startModelsServe(t)

	apiSend(t, http.MethodPatch, base, "/api/models", `{"paused":true}`)
	if m := getModels(t, base); !m.Paused || m.AllowPaidBulk || m.DailyLimit != 25 || workProvider(m, "reading") != "localbox" {
		t.Errorf("after pausing: %+v", m)
	}
	apiSend(t, http.MethodPatch, base, "/api/models", `{"allowPaidBulk":true}`)
	if m := getModels(t, base); !m.AllowPaidBulk || !m.Paused || m.DailyLimit != 25 {
		t.Errorf("after allowing paid bulk work: %+v", m)
	}
	apiSend(t, http.MethodPatch, base, "/api/models", `{"dailyLimit":0}`)
	if m := getModels(t, base); m.DailyLimit != 0 || !m.Paused || !m.AllowPaidBulk {
		t.Errorf("after clearing the daily limit: %+v", m)
	}
	apiSend(t, http.MethodPatch, base, "/api/models", `{"work":{"reading":"cloudbox"}}`)
	if m := getModels(t, base); workProvider(m, "reading") != "cloudbox" ||
		workProvider(m, "shortlist_check") != "localbox" || workProvider(m, "preparation") != "cloudbox" ||
		!m.Paused || !m.AllowPaidBulk || m.DailyLimit != 0 {
		t.Errorf("after changing reading provider: %+v", m)
	}
}

func TestAnUnknownProviderOrKindOfWorkIsRefusedAndChangesNothing(t *testing.T) {
	base, _ := startModelsServe(t)

	for payload, message := range map[string]string{
		`{"work":{"reading":"nowhere"}}`:   "nowhere",
		`{"work":{"dreaming":"localbox"}}`: "dreaming",
		`{"dailyLimit":-1}`:                "0 or more",
	} {
		result, status := apiSend(t, http.MethodPatch, base, "/api/models", payload)
		if status != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(result["error"]), message) {
			t.Errorf("PATCH %s: status %d, body %v", payload, status, result)
		}
	}
	if m := getModels(t, base); workProvider(m, "reading") != "localbox" || m.DailyLimit != 25 {
		t.Errorf("a refused change altered the settings: %+v", m)
	}
}

func TestTheDaysCountHoldsOnlyTodaysBackgroundCalls(t *testing.T) {
	base, dir := startModelsServe(t)

	database, err := db.Open(filepath.Join(dir, "sifty.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	now := time.Now().UTC()
	for _, call := range []struct {
		at         time.Time
		background int
	}{{now, 1}, {now, 0}, {now.Add(-26 * time.Hour), 1}} {
		_, err := database.Exec(`INSERT INTO model_call (created_at,kind,background,provider,model,chars_in,tokens_in,chars_out,tokens_out,duration_ms,outcome)`+
			` VALUES (?,'reading',?,'localbox','tiny-local',0,0,0,0,0,'ok')`, call.at.Format(time.RFC3339), call.background)
		if err != nil {
			t.Fatalf("insert call: %v", err)
		}
	}
	if got := getModels(t, base).TodayCalls; got != 1 {
		t.Errorf("today's background calls: got %d, want 1", got)
	}
}

func TestTheSeparatePauseAddressIsNoLongerUsed(t *testing.T) {
	base, _ := startModelsServe(t)

	_, status := apiSend(t, http.MethodPut, base, "/api/models/pause", `{"paused":true}`)
	if status != http.StatusNotFound || getModels(t, base).Paused {
		t.Errorf("the old address answered %d and left the pause switch at %v", status, getModels(t, base).Paused)
	}
}

func TestPreciseReadingDefaultsByTheCostOfTheReadingProviderAndTheSwitchChangesOnlyItself(t *testing.T) {
	base, _ := startModelsServe(t)

	if m := getModels(t, base); !m.PreciseReading {
		t.Fatalf("Precise reading with a local Reading provider and no choice: %+v", m)
	}
	if result, status := apiSend(t, http.MethodPatch, base, "/api/models", `{"work":{"reading":"cloudbox"}}`); status != http.StatusOK {
		t.Fatalf("patch work: status %d, body %v", status, result)
	}
	if m := getModels(t, base); m.PreciseReading {
		t.Errorf("Precise reading with a paid Reading provider and no choice: %+v", m)
	}
	if result, status := apiSend(t, http.MethodPatch, base, "/api/models", `{"work":{"reading":"localbox"}}`); status != http.StatusOK {
		t.Fatalf("patch work back: status %d, body %v", status, result)
	}
	if result, status := apiSend(t, http.MethodPatch, base, "/api/models", `{"preciseReading":false}`); status != http.StatusOK {
		t.Fatalf("patch switch: status %d, body %v", status, result)
	}
	if m := getModels(t, base); m.PreciseReading || m.AllowPaidBulk || m.Paused || m.DailyLimit != 25 || workProvider(m, "reading") != "localbox" {
		t.Errorf("after turning Precise reading off with a local provider: %+v", m)
	}
}
