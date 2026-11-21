package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
)

const (
	stateQueued      = "queued"
	stateRunning     = "running"
	stateDone        = "done"
	stateFailed      = "failed"
	stateInterrupted = "interrupted"
)

var (
	errCancelled = errors.New("runner: job cancelled")
	errNotQueued = errors.New("runner: job is no longer queued")
)

var retryDelays = [2]time.Duration{30 * time.Second, 2 * time.Minute}

type Spec struct {
	Kind, Subject string
	Priority      int
	Params        json.RawMessage
}

type Kind struct {
	Name   string
	Run    func(ctx context.Context, j *Job) error
	Resume bool
	Serial bool
}

type waitError struct{ t time.Time }

func (w waitError) Error() string { return "try again after " + w.t.UTC().Format(time.RFC3339) }

func WaitUntil(t time.Time) error { return waitError{t: t} }

type progressJSON struct {
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Current string `json:"current,omitempty"`
}

type Job struct {
	ID        int64
	Kind      string
	Subject   string
	Priority  int
	State     string
	Attempts  int
	NotBefore time.Time
	Done      int
	Total     int
	Current   string
	Error     string
	Params    json.RawMessage
	Created   time.Time
	Updated   time.Time

	r *Runner
}

func (j *Job) Progress(done, total int, current string) {
	j.r.writeProgress(j, done, total, current)
}

func (j *Job) SaveParams(ctx context.Context, params json.RawMessage) error {
	_, err := j.r.db.ExecContext(ctx, `UPDATE job SET params=? WHERE id=?`, string(params), j.ID)
	return err
}

func (r *Runner) AdoptInterrupted(kind, subject string) (int64, bool, error) {
	var id int64
	err := r.db.QueryRow(
		`SELECT id FROM job WHERE kind=? AND subject=? AND state IN ('interrupted','cancelled') ORDER BY id DESC LIMIT 1`,
		kind, subject,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("runner: adopt interrupted: %w", err)
	}
	now := r.now().UTC().Format(time.RFC3339)
	res, err := r.db.Exec(
		`UPDATE job SET state='queued', not_before=?, updated=? WHERE id=? AND state IN ('interrupted','cancelled')`,
		now, now, id,
	)
	if err != nil {
		return 0, false, fmt.Errorf("runner: adopt interrupted: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n == 0 {
		return 0, false, nil
	}
	r.signal()
	return id, true, nil
}

func (j *Job) Lane(ctx context.Context, name string) (func(), error) {
	return j.r.acquireLane(ctx, name)
}

type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

func Permanent(err error) error { return permanentError{err: err} }

type lane struct {
	ch chan struct{}
}

func newLane(size int) *lane {
	ch := make(chan struct{}, size)
	for i := 0; i < size; i++ {
		ch <- struct{}{}
	}
	return &lane{ch: ch}
}

func (l *lane) acquire(ctx context.Context) error {
	select {
	case <-l.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *lane) release() {
	select {
	case l.ch <- struct{}{}:
	default:
	}
}

type jobState struct {
	mu     sync.Mutex
	progAt time.Time
	job    *Job
	stop   context.CancelCauseFunc
}

type Runner struct {
	db    *db.DB
	now   func() time.Time
	log   *slog.Logger
	kinds map[string]Kind
	lanes map[string]*lane

	mu     sync.Mutex
	active map[int64]*jobState

	wake chan struct{}
}

func New(database *db.DB, lanes map[string]int, now func() time.Time, log *slog.Logger) *Runner {
	ls := make(map[string]*lane, len(lanes))
	for name, size := range lanes {
		ls[name] = newLane(size)
	}
	return &Runner{
		db:     database,
		now:    now,
		log:    log,
		kinds:  make(map[string]Kind),
		lanes:  ls,
		active: make(map[int64]*jobState),
		wake:   make(chan struct{}, 1),
	}
}

func (r *Runner) Register(k Kind) {
	r.kinds[k.Name] = k
}

func (r *Runner) Start(ctx context.Context) error {
	r.deleteOldJobs()
	r.recoverInterrupted()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		r.dispatch(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

func (r *Runner) Enqueue(s Spec) (int64, error) {
	var id int64
	err := r.db.QueryRow(
		`SELECT id FROM job WHERE kind=? AND subject=? AND state IN ('queued','running') LIMIT 1`,
		s.Kind, s.Subject,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("runner: dedup check: %w", err)
	}

	now := r.now().UTC().Format(time.RFC3339)
	var params any
	if s.Params != nil {
		params = string(s.Params)
	}
	res, err := r.db.Exec(
		`INSERT INTO job (kind, subject, priority, state, attempts, not_before, progress, error, params, created, updated)
		 VALUES (?, ?, ?, 'queued', 0, ?, '{}', '', ?, ?, ?)`,
		s.Kind, s.Subject, s.Priority, now, params, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("runner: insert job: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("runner: last insert id: %w", err)
	}
	r.signal()
	return id, nil
}

func (r *Runner) Cancel(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.db.Exec(
		`UPDATE job SET state='cancelled', updated=? WHERE id=? AND state IN ('queued','running')`,
		r.now().UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return fmt.Errorf("runner: cancel: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if st := r.active[id]; st != nil {
			st.stop(errCancelled)
		}
	}
	return nil
}

func (r *Runner) Last(kind string) (Job, bool) {
	row := r.db.QueryRow(
		`SELECT id, kind, subject, priority, state, attempts, not_before,
		        progress, error, params, created, updated
		 FROM job WHERE kind=? ORDER BY created DESC, id DESC LIMIT 1`,
		kind,
	)
	j, err := scanJob(row)
	if err != nil {
		return Job{}, false
	}
	return j, true
}

func (r *Runner) Active(kinds ...string) []Job {
	cond := ""
	var args []any
	if len(kinds) > 0 {
		ph := strings.Repeat("?,", len(kinds))
		cond = " AND kind IN (" + ph[:len(ph)-1] + ")"
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	rows, err := r.db.Query(
		`SELECT id, kind, subject, priority, state, attempts, not_before,
		        progress, error, params, created, updated
		 FROM job WHERE state IN ('queued','running')`+cond+`
		 ORDER BY priority DESC, created ASC, id ASC`,
		args...)
	if err != nil {
		return nil
	}
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			continue
		}
		out = append(out, j)
	}
	rows.Close()
	return out
}

func (r *Runner) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) Wake() { r.signal() }

func (r *Runner) dispatch(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		j, err := r.pickOne()
		if err != nil || j == nil {
			return
		}
		j.r = r
		jobCtx, stop := context.WithCancelCause(ctx)
		r.mu.Lock()
		err = r.markRunning(j)
		if err == nil {
			r.active[j.ID] = &jobState{job: j, stop: stop}
		}
		r.mu.Unlock()
		if err != nil {
			stop(nil)
			if errors.Is(err, errNotQueued) {
				continue
			}
			return
		}
		kind := r.kinds[j.Kind]
		go func() {
			defer stop(nil)
			r.runJob(jobCtx, j, kind)
		}()
	}
}

func (r *Runner) pickOne() (*Job, error) {
	if len(r.kinds) == 0 {
		return nil, nil
	}

	now := r.now().UTC().Format(time.RFC3339)
	names := make([]string, 0, len(r.kinds))
	args := make([]any, 0, 1+len(r.kinds))
	args = append(args, now)
	for name, k := range r.kinds {
		if k.Serial {
			var n int
			r.db.QueryRow(`SELECT count(*) FROM job WHERE kind=? AND state='running' LIMIT 1`, name).Scan(&n)
			if n > 0 {
				continue
			}
		}
		names = append(names, "?")
		args = append(args, name)
	}
	if len(names) == 0 {
		return nil, nil
	}
	ph := strings.Join(names, ",")

	row := r.db.QueryRow(
		`SELECT id, kind, subject, priority, state, attempts, not_before,
		        progress, error, params, created, updated
		 FROM job
		 WHERE state = 'queued' AND not_before <= ? AND kind IN (`+ph+`)
		 ORDER BY priority DESC, created ASC, id ASC
		 LIMIT 1`,
		args...,
	)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *Runner) markRunning(j *Job) error {
	now := r.now().UTC().Format(time.RFC3339)
	j.Attempts++
	res, err := r.db.Exec(
		`UPDATE job SET state='running', attempts=?, updated=? WHERE id=? AND state='queued'`,
		j.Attempts, now, j.ID,
	)
	if err != nil {
		return fmt.Errorf("runner: mark running: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotQueued
	}
	j.State = stateRunning
	j.Updated = r.now()
	return nil
}

func (r *Runner) runJob(ctx context.Context, j *Job, kind Kind) {
	var runErr error
	func() {
		defer func() {
			if p := recover(); p != nil {
				stack := debug.Stack()
				r.log.Error("runner: panic in job",
					"id", j.ID, "kind", j.Kind, "subject", j.Subject,
					"panic", fmt.Sprintf("%v", p), "stack", string(stack))
				runErr = fmt.Errorf("panic: %v", p)
			}
		}()
		runErr = kind.Run(ctx, j)
	}()
	r.finishJob(ctx, j, runErr)
}

func (r *Runner) finishJob(ctx context.Context, j *Job, runErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, j.ID)

	if errors.Is(context.Cause(ctx), errCancelled) {
		return
	}
	if runErr != nil && ctx.Err() != nil {
		return
	}

	now := r.now()
	ts := now.UTC().Format(time.RFC3339)

	var dbErr error
	if runErr == nil {
		j.State = stateDone
		finalProg, _ := json.Marshal(progressJSON{Done: j.Done, Total: j.Total, Current: j.Current})
		_, dbErr = r.db.Exec(`UPDATE job SET state='done', progress=?, updated=? WHERE id=?`, string(finalProg), ts, j.ID)
	} else if isPermanent(runErr) {
		j.State = stateFailed
		j.Error = runErr.Error()
		_, dbErr = r.db.Exec(
			`UPDATE job SET state='failed', error=?, updated=? WHERE id=?`, j.Error, ts, j.ID)
	} else {
		var we waitError
		if errors.As(runErr, &we) {
			nb := we.t
			nbStr := nb.UTC().Format(time.RFC3339)
			j.State = stateQueued
			j.NotBefore = nb
			j.Error = runErr.Error()
			j.Attempts--
			_, dbErr = r.db.Exec(
				`UPDATE job SET state='queued', attempts=?, error=?, not_before=?, updated=? WHERE id=?`,
				j.Attempts, j.Error, nbStr, ts, j.ID)
			if dbErr == nil {
				_, dbErr = r.db.Exec(
					`UPDATE job SET not_before=?, updated=? WHERE kind=? AND state='queued' AND id!=?`,
					nbStr, ts, j.Kind, j.ID)
			}
		} else {
			j.Error = runErr.Error()
			if j.Attempts >= 3 {
				j.State = stateFailed
				_, dbErr = r.db.Exec(
					`UPDATE job SET state='failed', error=?, updated=? WHERE id=?`, j.Error, ts, j.ID)
			} else {
				delay := retryDelays[j.Attempts-1]
				nb := now.Add(delay)
				j.State = stateQueued
				j.NotBefore = nb
				_, dbErr = r.db.Exec(
					`UPDATE job SET state='queued', error=?, not_before=?, updated=? WHERE id=?`,
					j.Error, nb.UTC().Format(time.RFC3339), ts, j.ID)
			}
		}
	}
	if dbErr != nil {
		r.log.Error("runner: failed to update job state", "id", j.ID, "state", j.State, "error", dbErr)
	}

	r.signal()
}

func (r *Runner) writeProgress(j *Job, done, total int, current string) {
	r.mu.Lock()
	state := r.active[j.ID]
	r.mu.Unlock()

	if state == nil {
		return
	}

	state.mu.Lock()
	j.Done = done
	j.Total = total
	j.Current = current
	now := r.now()
	writeDB := now.Sub(state.progAt) >= time.Second
	if writeDB {
		state.progAt = now
	}
	state.mu.Unlock()

	if writeDB {
		prog, _ := json.Marshal(progressJSON{Done: done, Total: total, Current: current})
		if _, err := r.db.Exec(
			`UPDATE job SET progress=?, updated=? WHERE id=?`,
			string(prog), now.UTC().Format(time.RFC3339), j.ID,
		); err != nil {
			r.log.Warn("runner: failed to write job progress", "id", j.ID, "error", err)
		}
	}
}

func (r *Runner) acquireLane(ctx context.Context, name string) (func(), error) {
	l, ok := r.lanes[name]
	if !ok {
		return func() {}, nil
	}
	var once sync.Once
	rel := func() { once.Do(func() { l.release() }) }
	if err := l.acquire(ctx); err != nil {
		return func() {}, err
	}
	return rel, nil
}

func (r *Runner) deleteOldJobs() {
	cutoff := r.now().UTC().Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := r.db.Exec(
		`DELETE FROM job WHERE state IN ('done','failed','interrupted','cancelled') AND created < ?`, cutoff,
	); err != nil {
		r.log.Warn("runner: failed to delete old jobs", "error", err)
	}
}

func (r *Runner) recoverInterrupted() {
	rows, err := r.db.Query(`SELECT id, kind, attempts FROM job WHERE state = 'running'`)
	if err != nil {
		r.log.Error("runner: failed to query interrupted jobs", "error", err)
		return
	}

	type info struct {
		id       int64
		kind     string
		attempts int
	}
	var jobs []info
	for rows.Next() {
		var inf info
		if err := rows.Scan(&inf.id, &inf.kind, &inf.attempts); err != nil {
			continue
		}
		jobs = append(jobs, inf)
	}
	rows.Close()

	now := r.now().UTC().Format(time.RFC3339)
	for _, inf := range jobs {
		k, registered := r.kinds[inf.kind]
		var dbErr error
		if registered && k.Resume {
			if inf.attempts < 3 {
				_, dbErr = r.db.Exec(
					`UPDATE job SET state='queued', not_before=?, updated=? WHERE id=?`, now, now, inf.id)
			} else {
				_, dbErr = r.db.Exec(
					`UPDATE job SET state='failed', updated=? WHERE id=?`, now, inf.id)
			}
		} else {
			_, dbErr = r.db.Exec(
				`UPDATE job SET state='interrupted', updated=? WHERE id=?`, now, inf.id)
		}
		if dbErr != nil {
			r.log.Error("runner: failed to recover interrupted job", "id", inf.id, "error", dbErr)
		}
	}
}

func isPermanent(err error) bool {
	var pe permanentError
	return errors.As(err, &pe)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanJob(s scanner) (Job, error) {
	var j Job
	var notBefore, created, updated, progress string
	var params sql.NullString

	err := s.Scan(
		&j.ID, &j.Kind, &j.Subject, &j.Priority, &j.State,
		&j.Attempts, &notBefore, &progress, &j.Error,
		&params, &created, &updated,
	)
	if err != nil {
		return Job{}, err
	}

	j.NotBefore, _ = time.Parse(time.RFC3339, notBefore)
	j.Created, _ = time.Parse(time.RFC3339, created)
	j.Updated, _ = time.Parse(time.RFC3339, updated)

	var prog progressJSON
	if progress != "" && progress != "{}" {
		json.Unmarshal([]byte(progress), &prog)
	}
	j.Done = prog.Done
	j.Total = prog.Total
	j.Current = prog.Current

	if params.Valid && params.String != "" {
		j.Params = json.RawMessage(params.String)
	}

	return j, nil
}
