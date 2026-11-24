package models_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
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

func openTestStore(t *testing.T, cfg config.Config) *config.Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sifty.yaml")
	store, _, err := config.Open(p)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	store.Update(func(c *config.Config) error {
		*c = cfg
		return nil
	})
	return store
}

func providerConfig(provType, endpoint, model, cost string) config.ProviderConfig {
	return config.ProviderConfig{Type: provType, Endpoint: endpoint, Model: model, Cost: cost, Context: 8192}
}

func ollamaReply(text string) []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"response": text, "done": true, "prompt_eval_count": 5, "eval_count": 3,
	})
	return b
}

func TestOllamaAdapterLoadsOnce(t *testing.T) {
	loadCount := 0
	genCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)
		if req["prompt"] == "" || req["prompt"] == nil {
			loadCount++
			w.Write(ollamaReply(""))
			return
		}
		genCount++
		w.Write(ollamaReply("hello"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local"}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate.Stop(context.Background())

	m := gate.For("reading", "user")
	m.Complete(context.Background(), models.Request{Prompt: "first"})
	m.Complete(context.Background(), models.Request{Prompt: "second"})

	if loadCount != 1 {
		t.Errorf("load requests: got %d, want 1", loadCount)
	}
	if genCount != 2 {
		t.Errorf("generate requests: got %d, want 2", genCount)
	}
}

func TestOllamaRefusesOverContextPrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ollamaReply("ok"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Providers["local"] = func() config.ProviderConfig {
		pc := providerConfig("ollama", srv.URL, "phi3", "local")
		pc.Context = 10
		return pc
	}()
	cfg.Work = config.WorkConfig{"reading": "local"}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate.Stop(context.Background())

	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "this prompt is too long"})
	if err == nil {
		t.Fatal("expected error for over-context prompt")
	}
}

func TestOllamaHonoursContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ollamaReply("done"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local"}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate.Stop(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := gate.For("reading", "user").Complete(ctx, models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected context-cancelled error")
	}
}

func TestOpenAISendsKeyAndParsesAnswer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]string{"content": "hello from openai"}}},
			"usage":   map[string]int{"prompt_tokens": 4, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	cfg := config.Defaults()
	pc := providerConfig("openai", srv.URL+"/v1", "gpt-4o-mini", "paid")
	pc.KeyEnv = "TESTPROVIDER_KEY"
	cfg.Providers = map[string]config.ProviderConfig{"remote": pc}
	cfg.Work = config.WorkConfig{"reading": "remote"}
	cfg.AllowPaidBulk = true
	store := openTestStore(t, cfg)
	secrets := map[string]string{"TESTPROVIDER_KEY": "sk-test-key"}
	gate := models.NewGate(openTestDB(t), store, false, secrets, nil)
	defer gate.Stop(context.Background())

	resp, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hello from openai" {
		t.Errorf("text: got %q, want %q", resp.Text, "hello from openai")
	}
	if gotAuth != "Bearer sk-test-key" {
		t.Errorf("Authorization: got %q, want %q", gotAuth, "Bearer sk-test-key")
	}
}

func TestUserCallBeforeBackgroundCall(t *testing.T) {
	var mu sync.Mutex
	var order []string
	blocker := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)
		if req["prompt"] == "" || req["prompt"] == nil {
			w.Write(ollamaReply(""))
			return
		}
		prompt, _ := req["prompt"].(string)
		if prompt == "blocking" {
			<-blocker
		}
		mu.Lock()
		order = append(order, prompt)
		mu.Unlock()
		w.Write(ollamaReply("ok"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local", "shortlist_check": "local"}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate.Stop(context.Background())

	done1 := make(chan error, 1)
	go func() {
		_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "blocking"})
		done1 <- err
	}()
	time.Sleep(20 * time.Millisecond)

	_, err := gate.For("shortlist_check", "background").Complete(context.Background(), models.Request{Prompt: "bg"})
	if _, ok := err.(models.WaitError); !ok {
		t.Errorf("background call while user waiting: got %v, want WaitError", err)
	}

	close(blocker)
	<-done1
}

func TestBackgroundWaitsWhilePaused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ollamaReply("ok"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local"}
	store := openTestStore(t, cfg)
	d := openTestDB(t)
	gate := models.NewGate(d, store, false, nil, nil)
	defer gate.Stop(context.Background())

	gate.SetPaused(context.Background(), true)
	_, err := gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "hello"})
	we, ok := err.(models.WaitError)
	if !ok {
		t.Fatalf("expected WaitError when paused, got %v", err)
	}
	if we.RetryAfter.Before(time.Now()) {
		t.Error("WaitError retry time should be in the future")
	}
}

func TestBackgroundWaitsAtDailyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ollamaReply("ok"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local"}
	cfg.DailyModelLimit = 2
	store := openTestStore(t, cfg)
	d := openTestDB(t)
	gate := models.NewGate(d, store, false, nil, nil)
	defer gate.Stop(context.Background())

	gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "first"})
	gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "second"})

	_, err := gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "third"})
	_, ok := err.(models.WaitError)
	if !ok {
		t.Fatalf("expected WaitError at daily limit, got %v", err)
	}
}

func TestDailyLimitOfZeroAllowsUnlimitedBackgroundCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ollamaReply("ok"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local"}
	cfg.DailyModelLimit = 0
	store := openTestStore(t, cfg)
	d := openTestDB(t)
	gate := models.NewGate(d, store, false, nil, nil)
	defer gate.Stop(context.Background())

	for i := 0; i < 3; i++ {
		_, err := gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "call"})
		if err != nil {
			t.Fatalf("background call %d with zero daily limit: %v", i+1, err)
		}
	}
}

func TestDailyLimitReducedOnRunningGateIsEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ollamaReply("ok"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local"}
	cfg.DailyModelLimit = 0
	store := openTestStore(t, cfg)
	d := openTestDB(t)
	gate := models.NewGate(d, store, false, nil, nil)
	defer gate.Stop(context.Background())

	gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "first"})
	gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "second"})

	if err := store.Update(func(c *config.Config) error { c.DailyModelLimit = 2; return nil }); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	_, err := gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "third"})
	if _, ok := err.(models.WaitError); !ok {
		t.Fatalf("third background call after limit set to 2 on running gate: got %v, want WaitError", err)
	}
}

func TestBulkOnPaidRefusedUnlessAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]string{"content": "ok"}}},
			"usage":   map[string]int{},
		})
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"remote": providerConfig("openai", srv.URL, "gpt-4o", "paid")}
	cfg.Work = config.WorkConfig{"reading": "remote"}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate.Stop(context.Background())

	_, err := gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error for bulk on paid without permission")
	}

	store.Update(func(c *config.Config) error { c.AllowPaidBulk = true; return nil })
	gate2 := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate2.Stop(context.Background())
	_, err = gate2.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("expected success with AllowPaidBulk=true, got %v", err)
	}
}

func TestRevokedPaidBulkPermissionRefusesNextCallOnSameGate(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqCount, 1)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]string{"content": "ok"}}},
			"usage":   map[string]int{},
		})
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"remote": providerConfig("openai", srv.URL, "gpt-4o", "paid")}
	cfg.Work = config.WorkConfig{"reading": "remote"}
	cfg.AllowPaidBulk = true
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate.Stop(context.Background())

	_, err := gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "first"})
	if err != nil {
		t.Fatalf("first background call with paid bulk allowed: %v", err)
	}

	if err := store.Update(func(c *config.Config) error { c.AllowPaidBulk = false; return nil }); err != nil {
		t.Fatalf("store.Update: %v", err)
	}

	_, err = gate.For("reading", "background").Complete(context.Background(), models.Request{Prompt: "second"})
	if !errors.Is(err, models.ErrBulkOnPaid) {
		t.Fatalf("second background call after revoking paid bulk permission: got %v, want ErrBulkOnPaid", err)
	}
	if n := atomic.LoadInt32(&reqCount); n != 1 {
		t.Errorf("paid provider received %d requests, want 1 (the refused call must not reach the provider)", n)
	}
}

func TestModelCallJournalRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]string{"content": "result"}}},
			"usage":   map[string]int{"prompt_tokens": 3, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"remote": providerConfig("openai", srv.URL, "gpt-4o-mini", "paid")}
	cfg.Work = config.WorkConfig{"reading": "remote"}
	cfg.AllowPaidBulk = true
	store := openTestStore(t, cfg)
	d := openTestDB(t)
	gate := models.NewGate(d, store, false, nil, nil)
	defer gate.Stop(context.Background())

	gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "test"})

	var n int
	d.QueryRowContext(context.Background(), `SELECT count(*) FROM model_call WHERE kind='reading' AND outcome='ok'`).Scan(&n)
	if n != 1 {
		t.Errorf("model_call rows: got %d, want 1", n)
	}
}

func TestOfflineMakesNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write(ollamaReply("ok"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local"}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, true, nil, nil)
	defer gate.Stop(context.Background())

	_, err := gate.For("reading", "user").Complete(context.Background(), models.Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error in offline mode")
	}
	if called {
		t.Error("server was called in offline mode")
	}
}

func TestCheckWithFailingEndpointReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)
		if req["prompt"] == "" || req["prompt"] == nil {
			w.Write(ollamaReply(""))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate.Stop(context.Background())

	state, err := gate.Check(context.Background(), "local")
	if err == nil {
		t.Fatal("expected error for failing endpoint")
	}
	if state == "ok" {
		t.Error("state should not be ok for failing endpoint")
	}
}

func TestOllamaSendsFormatAndThink(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body["prompt"] == "" || body["prompt"] == nil {
			w.Write(ollamaReply(""))
			return
		}
		w.Write(ollamaReply(`{"requirements":[],"conditions":{}}`))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	cfg.Work = config.WorkConfig{"reading": "local"}
	store := openTestStore(t, cfg)
	gate := models.NewGate(openTestDB(t), store, false, nil, nil)
	defer gate.Stop(context.Background())

	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	gate.For("reading", "user").Complete(context.Background(), models.Request{
		Prompt: "test vacancy",
		Schema: schema,
		Think:  "off",
	})

	if body["format"] == nil {
		t.Error("ollama request missing format field")
	}
	thinkVal, hasThink := body["think"]
	if !hasThink {
		t.Error("ollama request missing think field")
	} else if thinkVal != false {
		t.Errorf("ollama request think = %v, want false", thinkVal)
	}
}

func TestSecretsFileLoaded(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("MYHOST_KEY=secret123\n"), 0600)
	secrets, err := models.LoadSecrets(filepath.Join(dir, "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if secrets["MYHOST_KEY"] != "secret123" {
		t.Errorf("MYHOST_KEY: got %q", secrets["MYHOST_KEY"])
	}
}

func TestCheckJournalsNonZeroDuration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)
		if req["prompt"] == "" || req["prompt"] == nil {
			w.Write(ollamaReply(""))
			return
		}
		time.Sleep(5 * time.Millisecond)
		w.Write(ollamaReply("OK"))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": providerConfig("ollama", srv.URL, "phi3", "local")}
	store := openTestStore(t, cfg)
	d := openTestDB(t)
	gate := models.NewGate(d, store, false, nil, nil)
	defer gate.Stop(context.Background())

	gate.Check(context.Background(), "local")

	var durMS int
	d.QueryRowContext(context.Background(), `SELECT duration_ms FROM model_call WHERE kind='check'`).Scan(&durMS)
	if durMS == 0 {
		t.Error("check journal: duration_ms is 0, want non-zero")
	}
}

type gateFixture struct {
	gate     *models.Gate
	db       *db.DB
	requests atomic.Int32
	failing  atomic.Bool
	offset   atomic.Int64
}

func newGateFixture(t *testing.T, contextChars, dailyLimit int, handler http.HandlerFunc) *gateFixture {
	t.Helper()
	f := &gateFixture{}
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			f.requests.Add(1)
			if f.failing.Load() {
				http.Error(w, "down", http.StatusInternalServerError)
				return
			}
			w.Write(ollamaReply("ok"))
		}
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	pc := providerConfig("ollama", srv.URL, "phi3", "local")
	if contextChars > 0 {
		pc.Context = contextChars
	}
	cfg := config.Defaults()
	cfg.Providers = map[string]config.ProviderConfig{"local": pc}
	cfg.Work = config.WorkConfig{"reading": "local"}
	cfg.DailyModelLimit = dailyLimit
	f.db = openTestDB(t)
	f.gate = models.NewGate(f.db, openTestStore(t, cfg), false, nil, nil)
	t.Cleanup(func() { f.gate.Stop(context.Background()) })
	start := time.Now()
	f.gate.SetClock(func() time.Time { return start.Add(time.Duration(f.offset.Load())) })
	return f
}

func (f *gateFixture) advance(d time.Duration) { f.offset.Add(int64(d)) }

func (f *gateFixture) background(ctx context.Context, prompt string) (models.Response, error) {
	return f.gate.For("reading", "background").Complete(ctx, models.Request{Prompt: prompt})
}

func (f *gateFixture) providerState() string {
	return f.gate.Status().Providers["local"].State
}

func TestPromptTooLongFailsOnlyThatCallAndLeavesProviderUsable(t *testing.T) {
	f := newGateFixture(t, 20, 0, nil)

	_, err := f.background(context.Background(), "this prompt is much too long for the context")
	if !errors.Is(err, models.ErrPromptTooLong) {
		t.Fatalf("expected ErrPromptTooLong, got %v", err)
	}
	if s := f.providerState(); s != "" && s != "ok" {
		t.Fatalf("provider state after a too long prompt = %q, want ok", s)
	}

	resp, err := f.background(context.Background(), "short")
	if err != nil {
		t.Fatalf("next background call failed: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("next call reply = %q, want the model's answer", resp.Text)
	}
}

func TestUnreachableProviderHoldsBackgroundCallsAndIsProbedEveryFiveMinutes(t *testing.T) {
	f := newGateFixture(t, 0, 0, nil)
	f.failing.Store(true)

	if _, err := f.background(context.Background(), "first"); err == nil {
		t.Fatal("expected the failing provider to return an error")
	} else if _, wait := err.(models.WaitError); wait {
		t.Fatalf("the first failure should be the provider's error, got %v", err)
	}
	if s := f.providerState(); s == "" || s == "ok" {
		t.Fatalf("provider state after a failure = %q, want the failure", s)
	}

	expectHeld := func(label string) {
		t.Helper()
		seen := f.requests.Load()
		if _, err := f.background(context.Background(), label); err == nil {
			t.Fatalf("%s: expected a wait", label)
		} else if _, wait := err.(models.WaitError); !wait {
			t.Fatalf("%s: expected WaitError, got %v", label, err)
		}
		if f.requests.Load() != seen {
			t.Fatalf("%s: a held provider was called", label)
		}
	}

	f.advance(4 * time.Minute)
	expectHeld("inside five minutes")

	f.advance(2 * time.Minute)
	seen := f.requests.Load()
	if _, err := f.background(context.Background(), "failing probe"); err == nil {
		t.Fatal("expected the probe to fail")
	} else if _, wait := err.(models.WaitError); wait {
		t.Fatalf("the probe should reach the provider, got %v", err)
	}
	if f.requests.Load() == seen {
		t.Fatal("the probe did not reach the provider")
	}

	f.advance(4 * time.Minute)
	expectHeld("inside five minutes of a failed probe")

	f.failing.Store(false)
	f.advance(2 * time.Minute)
	resp, err := f.background(context.Background(), "probe")
	if err != nil {
		t.Fatalf("probe after the second hold failed: %v", err)
	}
	if resp.Text != "ok" {
		t.Errorf("probe reply = %q", resp.Text)
	}
	if s := f.providerState(); s != "ok" {
		t.Errorf("provider state after a successful probe = %q, want ok", s)
	}
	if _, err := f.background(context.Background(), "work"); err != nil {
		t.Errorf("work after the probe should continue, got %v", err)
	}
}

func TestCancelledBackgroundCallCountsAgainstDailyLimit(t *testing.T) {
	var f *gateFixture
	started := make(chan struct{})
	f = newGateFixture(t, 0, 1, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)
		if p, _ := req["prompt"].(string); p == "" {
			w.Write(ollamaReply(""))
			return
		}
		f.requests.Add(1)
		close(started)
		<-r.Context().Done()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.background(ctx, "work")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected the cancelled call to fail")
	}

	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM model_call WHERE background = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("model_call rows for the cancelled call = %d, want 1", n)
	}
	if s := f.providerState(); s != "" && s != "ok" {
		t.Errorf("a cancelled call set the provider state to %q", s)
	}
	_, err := f.background(context.Background(), "next")
	if _, wait := err.(models.WaitError); !wait {
		t.Fatalf("expected WaitError at the daily limit, got %v", err)
	}
	if f.requests.Load() != 1 {
		t.Errorf("model requests = %d, want 1", f.requests.Load())
	}
}

func TestBackgroundWaitsWhenPauseSwitchCannotBeRead(t *testing.T) {
	f := newGateFixture(t, 0, 0, nil)
	f.db.Close()

	_, err := f.background(context.Background(), "work")
	if _, wait := err.(models.WaitError); !wait {
		t.Fatalf("expected WaitError with an unreadable database, got %v", err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("the model was called although the pause switch could not be read")
	}
}

func TestBackgroundWaitsWhenTodaysCallsCannotBeCounted(t *testing.T) {
	f := newGateFixture(t, 0, 5, nil)
	if _, err := f.db.Exec(`DROP TABLE model_call`); err != nil {
		t.Fatal(err)
	}

	_, err := f.background(context.Background(), "work")
	if _, wait := err.(models.WaitError); !wait {
		t.Fatalf("expected WaitError when calls cannot be counted, got %v", err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("the model was called although today's calls could not be counted")
	}
}
