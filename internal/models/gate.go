package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sifty-works/sifty-server/internal/config"
	"github.com/sifty-works/sifty-server/internal/db"
)

type Gate struct {
	db      *db.DB
	store   *config.Store
	offline bool
	secrets map[string]string
	logger  *slog.Logger
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]*providerEntry
}

type providerEntry struct {
	model    Model
	mu       chan struct{}
	waitUser int32

	stateMu         sync.RWMutex
	state           string
	stateErr        error
	stateAt         time.Time
	requestFailures int
	loading         bool
	loadStart       time.Time
	loadModel       string
	loaded          bool
}

func newEntry(m Model, modelName string) *providerEntry {
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	return &providerEntry{model: m, mu: ch, loadModel: modelName}
}

const providerRetryDelay = 5 * time.Minute
const requestFailuresBeforeHold = 3

func NewGate(database *db.DB, store *config.Store, offline bool, secrets map[string]string, logger *slog.Logger) *Gate {
	if secrets == nil {
		secrets = map[string]string{}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Gate{db: database, store: store, offline: offline, secrets: secrets, logger: logger, now: time.Now, entries: map[string]*providerEntry{}}
}

func (g *Gate) Ready(work string) error {
	cfg := g.store.Get()
	switch {
	case g.offline:
		return Tell(ErrOffline, work, "")
	case cfg.WorkProviderFor(work) == "":
		return Tell(ErrNoProvider, work, "")
	}
	return nil
}

func (g *Gate) For(kind, class string, vacancy ...int64) Model {
	c := &gatedCall{gate: g, kind: kind, background: class == "background"}
	if len(vacancy) > 0 {
		c.vacancyID = sql.NullInt64{Int64: vacancy[0], Valid: true}
	}
	return c
}

type gatedCall struct {
	gate       *Gate
	kind       string
	background bool
	vacancyID  sql.NullInt64
}

func (c *gatedCall) Complete(ctx context.Context, r Request) (Response, error) {
	if c.gate.offline {
		return Response{}, ErrOffline
	}
	cfg := c.gate.store.Get()
	name := cfg.WorkProviderFor(c.kind)
	if name == "" {
		return Response{}, fmt.Errorf("%w for %s", ErrNoProvider, c.kind)
	}
	pc, ok := cfg.Providers[name]
	if !ok {
		return Response{}, fmt.Errorf("provider %q not found", name)
	}

	if c.background && pc.Cost == "paid" && !cfg.AllowPaidBulk {
		return Response{}, ErrBulkOnPaid
	}

	entry, err := c.gate.getEntry(name, pc)
	if err != nil {
		return Response{}, err
	}

	if c.background {
		paused, err := c.gate.IsPaused(ctx)
		if err != nil || paused {
			return Response{}, WaitError{RetryAfter: c.gate.now().Add(providerRetryDelay)}
		}
		until, reached, err := c.gate.dailyLimit(ctx, cfg.DailyModelLimit)
		if err != nil {
			return Response{}, WaitError{RetryAfter: c.gate.now().Add(providerRetryDelay)}
		}
		if reached {
			return Response{}, WaitError{RetryAfter: until}
		}
		if atomic.LoadInt32(&entry.waitUser) > 0 {
			return Response{}, WaitError{RetryAfter: time.Now().Add(5 * time.Second)}
		}
		select {
		case <-entry.mu:
		default:
			return Response{}, WaitError{RetryAfter: time.Now().Add(5 * time.Second)}
		}
	} else {
		atomic.AddInt32(&entry.waitUser, 1)
		select {
		case <-entry.mu:
			atomic.AddInt32(&entry.waitUser, -1)
		case <-ctx.Done():
			atomic.AddInt32(&entry.waitUser, -1)
			return Response{}, ctx.Err()
		}
	}
	defer func() { entry.mu <- struct{}{} }()

	if c.background {
		if h, held := entry.hold(c.gate.now()); held {
			return Response{}, WaitError{RetryAfter: h.Until}
		}
	}

	start := time.Now()
	entry, err = c.gate.loadIfNeeded(ctx, entry, pc)
	if err != nil {
		failure := c.gate.updateState(entry, err)
		c.gate.journal(ctx, c.kind, name, pc.Model, c.background, len(r.Prompt), 0, 0, 0, int(time.Since(start).Milliseconds()), err.Error(), c.vacancyID)
		return Response{}, failure
	}

	resp, callErr := entry.model.Complete(ctx, r)
	dur := time.Since(start)

	outcome := "ok"
	if callErr != nil {
		outcome = callErr.Error()
	}
	failure := c.gate.updateState(entry, callErr)
	c.gate.journal(ctx, c.kind, name, pc.Model, c.background,
		len(r.Prompt), resp.InTokens, len(resp.Text), resp.OutTokens,
		int(dur.Milliseconds()), outcome, c.vacancyID)
	return resp, failure
}

func (g *Gate) loadIfNeeded(ctx context.Context, e *providerEntry, pc config.ProviderConfig) (*providerEntry, error) {
	loader, ok := e.model.(interface{ Load(context.Context) error })
	if !ok || e.loaded {
		return e, nil
	}
	e.stateMu.Lock()
	e.loading = true
	e.loadStart = time.Now()
	e.stateMu.Unlock()
	err := loader.Load(ctx)
	e.stateMu.Lock()
	e.loading = false
	if err == nil {
		e.loaded = true
	}
	e.stateMu.Unlock()
	return e, err
}

type Hold struct {
	Until time.Time
	Why   error
}

func (e *providerEntry) hold(now time.Time) (Hold, bool) {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	if e.state == "" || e.state == "ok" {
		return Hold{}, false
	}
	until := e.stateAt.Add(providerRetryDelay)
	return Hold{Until: until, Why: e.stateErr}, now.Before(until)
}

func (g *Gate) updateState(e *providerEntry, err error) error {
	var re requestError
	isRequest := errors.As(err, &re)
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrPromptTooLong) {
		return err
	}
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	switch {
	case err == nil:
		e.state = "ok"
		e.stateErr = nil
		e.requestFailures = 0
	case isRequest:
		e.requestFailures++
		if e.requestFailures >= requestFailuresBeforeHold {
			e.state = err.Error()
			e.stateErr = fmt.Errorf("%w: %w", ErrUnusableAnswers, err)
			e.stateAt = g.now()
		}
	default:
		e.state = err.Error()
		e.stateAt = g.now()
		e.stateErr = fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
		return e.stateErr
	}
	return err
}

func (g *Gate) Held(provider string) (Hold, bool) {
	g.mu.Lock()
	e, ok := g.entries[provider]
	g.mu.Unlock()
	if !ok {
		return Hold{}, false
	}
	return e.hold(g.now())
}

func (g *Gate) DailyLimitUntil(ctx context.Context) (time.Time, bool, error) {
	return g.dailyLimit(ctx, g.store.Get().DailyModelLimit)
}

func (g *Gate) dailyLimit(ctx context.Context, limit int) (time.Time, bool, error) {
	if limit <= 0 {
		return time.Time{}, false, nil
	}
	n, err := countTodayCalls(ctx, g.db)
	if err != nil {
		return time.Time{}, false, err
	}
	next := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	return next, n >= limit, nil
}

func (g *Gate) Check(ctx context.Context, name string) (string, error) {
	if g.offline {
		return "Offline", nil
	}
	cfg := g.store.Get()
	pc, ok := cfg.Providers[name]
	if !ok {
		return "", fmt.Errorf("provider %q not found", name)
	}
	entry, err := g.getEntry(name, pc)
	if err != nil {
		return err.Error(), err
	}
	atomic.AddInt32(&entry.waitUser, 1)
	select {
	case <-entry.mu:
		atomic.AddInt32(&entry.waitUser, -1)
	case <-ctx.Done():
		atomic.AddInt32(&entry.waitUser, -1)
		return "", ctx.Err()
	}
	defer func() { entry.mu <- struct{}{} }()
	start := time.Now()
	entry, err = g.loadIfNeeded(ctx, entry, pc)
	if err != nil {
		g.updateState(entry, err)
		g.journal(ctx, "check", name, pc.Model, false, 0, 0, 0, 0, int(time.Since(start).Milliseconds()), err.Error(), sql.NullInt64{})
		return err.Error(), err
	}
	const checkPrompt = "Reply with the single word: OK"
	resp, callErr := entry.model.Complete(ctx, Request{Prompt: checkPrompt})
	dur := time.Since(start)
	g.updateState(entry, callErr)
	outcome := "ok"
	if callErr != nil {
		outcome = callErr.Error()
	}
	g.journal(ctx, "check", name, pc.Model, false, len(checkPrompt), resp.InTokens, len(resp.Text), resp.OutTokens, int(dur.Milliseconds()), outcome, sql.NullInt64{})
	if callErr != nil {
		return callErr.Error(), callErr
	}
	return "ok", nil
}

func (g *Gate) getEntry(name string, pc config.ProviderConfig) (*providerEntry, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.entries[name]; ok {
		return e, nil
	}
	ctor, ok := adapters[pc.Type]
	if !ok {
		return nil, fmt.Errorf("unknown provider type %q", pc.Type)
	}
	m, err := ctor(pc.Endpoint, pc.Model, pc.KeyEnv, int(time.Duration(pc.Timeout).Milliseconds()), pc.Context, g.secrets)
	if err != nil {
		return nil, err
	}
	e := newEntry(m, pc.Model)
	g.entries[name] = e
	return e, nil
}

func (g *Gate) Status() *ModelsStatus {
	cfg := g.store.Get()
	providers := make(map[string]ProviderState, len(cfg.Providers))
	g.mu.Lock()
	for name := range cfg.Providers {
		e, ok := g.entries[name]
		if !ok {
			providers[name] = ProviderState{State: ""}
			continue
		}
		e.stateMu.RLock()
		ps := ProviderState{State: e.state}
		if e.loading {
			ps.Loading = &LoadingInfo{Model: e.loadModel, StartedAt: e.loadStart.UTC().Format(time.RFC3339)}
		}
		e.stateMu.RUnlock()
		providers[name] = ps
	}
	g.mu.Unlock()
	paused, _ := g.IsPaused(context.Background())
	return &ModelsStatus{Providers: providers, Paused: paused}
}

func (g *Gate) Store() *config.Store { return g.store }

func (g *Gate) TodayBackgroundCalls(ctx context.Context) int {
	n, _ := countTodayCalls(ctx, g.db)
	return n
}

func (g *Gate) IsPaused(ctx context.Context) (bool, error) {
	if g.db == nil {
		return false, nil
	}
	var v string
	err := g.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='model_work_paused'`).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return v == "true", err
}

func (g *Gate) SetPaused(ctx context.Context, paused bool) error {
	v := "false"
	if paused {
		v = "true"
	}
	_, err := g.db.ExecContext(ctx, `INSERT OR REPLACE INTO settings (key,value) VALUES ('model_work_paused',?)`, v)
	return err
}

func (g *Gate) Stop(ctx context.Context) {
	g.mu.Lock()
	entries := make(map[string]*providerEntry, len(g.entries))
	for k, v := range g.entries {
		entries[k] = v
	}
	g.mu.Unlock()
	for _, e := range entries {
		if u, ok := e.model.(interface{ Unload(context.Context) }); ok {
			u.Unload(ctx)
		}
	}
}

func countTodayCalls(ctx context.Context, database *db.DB) (int, error) {
	if database == nil {
		return 0, nil
	}
	today := time.Now().UTC().Format("2006-01-02")
	var n int
	err := database.QueryRowContext(ctx, `SELECT count(*) FROM model_call WHERE background = 1 AND created_at >= ?`, today+"T00:00:00Z").Scan(&n)
	return n, err
}

func (g *Gate) journal(ctx context.Context, kind, provider, model string, background bool, charsIn, tokensIn, charsOut, tokensOut, durMS int, outcome string, vacancyID sql.NullInt64) {
	if g.db == nil {
		return
	}
	bg := 0
	if background {
		bg = 1
	}
	_, err := g.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO model_call (created_at,kind,background,provider,model,chars_in,tokens_in,chars_out,tokens_out,duration_ms,outcome,vacancy_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339), kind, bg, provider, model, charsIn, tokensIn, charsOut, tokensOut, durMS, outcome, vacancyID,
	)
	if err != nil {
		g.logger.Error("model call not recorded", "kind", kind, "provider", provider, "err", err)
	}
}
