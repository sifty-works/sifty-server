package runner_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/runner"
)

var base = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), db.Migrations)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(t time.Time) *clock        { return &clock{now: t} }
func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }
func (c *clock) Set(t time.Time)         { c.mu.Lock(); c.now = t; c.mu.Unlock() }

func newRunner(d *db.DB, lanes map[string]int, cl *clock) *runner.Runner {
	now := func() time.Time { return base }
	if cl != nil {
		now = cl.Now
	}
	return runner.New(d, lanes, now, slog.Default())
}

func startRunner(t *testing.T, r *runner.Runner) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := r.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("runner.Start: %v", err)
		}
	}()
	return cancel
}

func waitJob(t *testing.T, r *runner.Runner, kind string) runner.Job {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if j, ok := r.Last(kind); ok && (j.State == "done" || j.State == "failed") {
			return j
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for job to complete")
			return runner.Job{}
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestDispatchRunsAllQueuedJobs(t *testing.T) {
	d := openDB(t)
	done := make(chan string, 3)
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "noop", Run: func(_ context.Context, j *runner.Job) error {
		done <- j.Subject
		return nil
	}})
	for _, s := range []string{"a", "b", "c"} {
		r.Enqueue(runner.Spec{Kind: "noop", Subject: s})
	}
	cancel := startRunner(t, r)
	defer cancel()
	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for len(seen) < 3 {
		select {
		case s := <-done:
			seen[s] = true
		case <-deadline:
			t.Fatalf("only %d of 3 jobs ran", len(seen))
		}
	}
}

func TestFutureNotBeforeJobWaits(t *testing.T) {
	d := openDB(t)
	cl := newClock(base)
	ran := make(chan string, 2)
	r := newRunner(d, nil, cl)
	r.Register(runner.Kind{Name: "nb", Run: func(_ context.Context, j *runner.Job) error { ran <- j.Subject; return nil }})
	r.Enqueue(runner.Spec{Kind: "nb", Subject: "ready"})
	cl.Advance(time.Hour)
	r.Enqueue(runner.Spec{Kind: "nb", Subject: "future"})
	cl.Set(base.Add(30 * time.Minute))
	cancel := startRunner(t, r)
	defer cancel()
	select {
	case s := <-ran:
		if s != "ready" {
			t.Errorf("first run: got %q, want ready", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ready job did not run")
	}
	var state string
	d.QueryRow(`SELECT state FROM job WHERE subject='future'`).Scan(&state)
	if state != "queued" {
		t.Errorf("future job state: got %q, want queued", state)
	}
}

func TestDeduplicationReturnsExistingID(t *testing.T) {
	d := openDB(t)
	hold := make(chan struct{})
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "dedup", Run: func(ctx context.Context, _ *runner.Job) error { <-hold; return nil }})
	id1, _ := r.Enqueue(runner.Spec{Kind: "dedup", Subject: "x"})
	if id2, _ := r.Enqueue(runner.Spec{Kind: "dedup", Subject: "x"}); id1 != id2 {
		t.Fatalf("dedup while queued: got %d, want %d", id2, id1)
	}
	cancel := startRunner(t, r)
	defer cancel()
	deadline := time.After(5 * time.Second)
	for {
		if j, ok := r.Last("dedup"); ok && j.State == "running" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for job to start running")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if id3, _ := r.Enqueue(runner.Spec{Kind: "dedup", Subject: "x"}); id1 != id3 {
		t.Fatalf("dedup while running: got %d, want %d", id3, id1)
	}
	close(hold)
	waitJob(t, r, "dedup")
	if id4, _ := r.Enqueue(runner.Spec{Kind: "dedup", Subject: "x"}); id4 == id1 {
		t.Error("after done: expected new id, got same")
	}
}

func TestUnregisteredKindStaysQueued(t *testing.T) {
	d := openDB(t)
	ts := base.UTC().Format(time.RFC3339)
	_, err := d.Exec(
		`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
		 VALUES ('unknown','x',0,'queued',0,?,'{}','',NULL,?,?)`, ts, ts, ts,
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	noop := make(chan struct{})
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "noop", Run: func(_ context.Context, _ *runner.Job) error { close(noop); return nil }})
	r.Enqueue(runner.Spec{Kind: "noop", Subject: "1"})
	cancel := startRunner(t, r)
	defer cancel()
	select {
	case <-noop:
	case <-time.After(2 * time.Second):
		t.Fatal("noop job did not run")
	}
	var state string
	d.QueryRow(`SELECT state FROM job WHERE kind='unknown'`).Scan(&state)
	if state != "queued" {
		t.Errorf("unregistered kind state: got %q, want queued", state)
	}
}

func TestLaneLimitsParallelism(t *testing.T) {
	d := openDB(t)
	acquired := make(chan struct{}, 3)
	proceed := make(chan struct{})
	r := newRunner(d, map[string]int{"net": 2}, nil)
	r.Register(runner.Kind{Name: "net-job", Run: func(ctx context.Context, j *runner.Job) error {
		rel, err := j.Lane(ctx, "net")
		if err != nil {
			return err
		}
		defer rel()
		acquired <- struct{}{}
		select {
		case <-proceed:
		case <-ctx.Done():
		}
		return nil
	}})
	for i := 0; i < 3; i++ {
		r.Enqueue(runner.Spec{Kind: "net-job", Subject: fmt.Sprintf("%d", i)})
	}
	cancel := startRunner(t, r)
	defer cancel()
	<-acquired
	<-acquired
	select {
	case <-acquired:
		t.Fatal("3rd job acquired lane before first two released")
	default:
	}
	close(proceed)
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("3rd job never acquired lane after release")
	}
}

func TestJobWaitingForLaneDoesNotBlockOtherLane(t *testing.T) {
	d := openDB(t)
	holdNet := make(chan struct{})
	cpuDone := make(chan struct{})
	r := newRunner(d, map[string]int{"net": 1, "cpu": 1}, nil)
	r.Register(runner.Kind{Name: "net-job", Run: func(ctx context.Context, j *runner.Job) error {
		rel, _ := j.Lane(ctx, "net")
		defer rel()
		select {
		case <-holdNet:
		case <-ctx.Done():
		}
		return nil
	}})
	r.Register(runner.Kind{Name: "cpu-job", Run: func(ctx context.Context, j *runner.Job) error {
		rel, _ := j.Lane(ctx, "cpu")
		defer rel()
		close(cpuDone)
		return nil
	}})
	r.Enqueue(runner.Spec{Kind: "net-job", Subject: "1", Priority: 1})
	r.Enqueue(runner.Spec{Kind: "cpu-job", Subject: "1"})
	cancel := startRunner(t, r)
	defer cancel()
	select {
	case <-cpuDone:
	case <-time.After(2 * time.Second):
		t.Fatal("cpu job blocked by net lane")
	}
	close(holdNet)
}

func TestRetryNotBeforeIsSetCorrectly(t *testing.T) {
	d := openDB(t)
	cl := newClock(base)
	var attempts atomic.Int32
	done := make(chan struct{})
	r := newRunner(d, nil, cl)
	r.Register(runner.Kind{Name: "fail", Run: func(_ context.Context, _ *runner.Job) error {
		if attempts.Add(1) == 1 {
			close(done)
		}
		return errors.New("transient")
	}})
	id, _ := r.Enqueue(runner.Spec{Kind: "fail", Subject: "1"})
	cancel := startRunner(t, r)
	defer cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("first attempt did not run")
	}
	time.Sleep(50 * time.Millisecond)
	var notBefore string
	d.QueryRow(`SELECT not_before FROM job WHERE id=?`, id).Scan(&notBefore)
	want := base.Add(30 * time.Second).UTC().Format(time.RFC3339)
	if notBefore != want {
		t.Errorf("not_before: got %q, want %q", notBefore, want)
	}
}

func TestPermanentErrorFailsImmediately(t *testing.T) {
	d := openDB(t)
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "perm", Run: func(_ context.Context, _ *runner.Job) error {
		return runner.Permanent(errors.New("done"))
	}})
	r.Enqueue(runner.Spec{Kind: "perm", Subject: "1"})
	cancel := startRunner(t, r)
	defer cancel()
	j := waitJob(t, r, "perm")
	if j.Attempts != 1 {
		t.Errorf("permanent: attempts = %d, want 1", j.Attempts)
	}
}

func TestThirdFailureMarksFailed(t *testing.T) {
	d := openDB(t)
	cl := newClock(base)
	r := newRunner(d, nil, cl)
	r.Register(runner.Kind{Name: "retry3", Run: func(_ context.Context, _ *runner.Job) error { return errors.New("always fails") }})
	r.Enqueue(runner.Spec{Kind: "retry3", Subject: "1"})
	cancel := startRunner(t, r)
	defer cancel()
	timeout := time.After(10 * time.Second)
	for {
		if j, ok := r.Last("retry3"); ok && j.State == "failed" {
			if j.Attempts != 3 {
				t.Errorf("failed at attempts=%d, want 3", j.Attempts)
			}
			return
		}
		select {
		case <-time.After(100 * time.Millisecond):
			cl.Advance(3 * time.Minute)
			r.Wake()
		case <-timeout:
			t.Fatal("job never reached failed after 3 attempts")
		}
	}
}

func TestPanicRecoveredAsError(t *testing.T) {
	d := openDB(t)
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "panic", Run: func(_ context.Context, _ *runner.Job) error { panic("test panic") }})
	r.Enqueue(runner.Spec{Kind: "panic", Subject: "1"})
	cancel := startRunner(t, r)
	defer cancel()
	deadline := time.After(5 * time.Second)
	for {
		if j, ok := r.Last("panic"); ok && j.Attempts > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out: panic job did not complete its first attempt")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestShutdownLeavesJobRunningForRestart(t *testing.T) {
	d := openDB(t)
	started := make(chan struct{})
	r1 := newRunner(d, nil, nil)
	r1.Register(runner.Kind{Name: "slow", Resume: true, Run: func(ctx context.Context, _ *runner.Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}})
	id, _ := r1.Enqueue(runner.Spec{Kind: "slow", Subject: "1"})
	ctx1, cancel1 := context.WithCancel(context.Background())
	go r1.Start(ctx1)
	<-started
	cancel1()
	time.Sleep(50 * time.Millisecond)
	var state string
	var attempts int
	d.QueryRow(`SELECT state, attempts FROM job WHERE id=?`, id).Scan(&state, &attempts)
	if state != "running" {
		t.Errorf("after shutdown: state=%q, want running", state)
	}
	if attempts != 1 {
		t.Errorf("after shutdown: attempts=%d, want 1", attempts)
	}
}

func TestRestart(t *testing.T) {
	cases := []struct {
		name     string
		resume   bool
		attempts int
		want     string
	}{
		{"resume below limit", true, 1, "queued"},
		{"resume at limit", true, 3, "failed"},
		{"no resume", false, 1, "interrupted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := openDB(t)
			ts := base.UTC().Format(time.RFC3339)
			_, err := d.Exec(
				`INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated)
				 VALUES ('k','x',0,'running',?,?,'{}','',NULL,?,?)`,
				tc.attempts, ts, ts, ts,
			)
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			r := newRunner(d, nil, nil)
			r.Register(runner.Kind{Name: "k", Resume: tc.resume, Run: func(_ context.Context, _ *runner.Job) error { return nil }})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r.Start(ctx)
			var state string
			d.QueryRow(`SELECT state FROM job WHERE kind='k' AND subject='x'`).Scan(&state)
			if state != tc.want {
				t.Errorf("state: got %q, want %q", state, tc.want)
			}
		})
	}
}

func TestOldFinishedJobsDeletedAtStart(t *testing.T) {
	d := openDB(t)
	old := base.Add(-91 * 24 * time.Hour).UTC().Format(time.RFC3339)
	now := base.UTC().Format(time.RFC3339)
	insert := `INSERT INTO job (kind,subject,priority,state,attempts,not_before,progress,error,params,created,updated) VALUES (?,'x',0,'done',1,?,'{}','',NULL,?,?)`
	d.Exec(insert, "old", now, old, now)
	d.Exec(insert, "recent", now, now, now)
	r := newRunner(d, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Start(ctx)
	var n int
	d.QueryRow(`SELECT count(*) FROM job WHERE kind='old'`).Scan(&n)
	if n != 0 {
		t.Errorf("old job not deleted: count=%d", n)
	}
	d.QueryRow(`SELECT count(*) FROM job WHERE kind='recent'`).Scan(&n)
	if n != 1 {
		t.Errorf("recent job deleted: count=%d", n)
	}
}

func TestStableOrderWithinOneSec(t *testing.T) {
	d := openDB(t)
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "ord", Run: func(_ context.Context, _ *runner.Job) error { return nil }})
	id1, _ := r.Enqueue(runner.Spec{Kind: "ord", Subject: "first"})
	id2, _ := r.Enqueue(runner.Spec{Kind: "ord", Subject: "second"})
	active := r.Active()
	if len(active) != 2 {
		t.Fatalf("want 2 active, got %d", len(active))
	}
	if active[0].ID != id1 || active[1].ID != id2 {
		t.Errorf("Active ids: [%d,%d], want [%d,%d]", active[0].ID, active[1].ID, id1, id2)
	}
}

func TestLastAndActive(t *testing.T) {
	d := openDB(t)
	hold := make(chan struct{})
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "q", Run: func(ctx context.Context, _ *runner.Job) error { <-hold; return nil }})
	r.Enqueue(runner.Spec{Kind: "q", Subject: "a"})
	r.Enqueue(runner.Spec{Kind: "q", Subject: "b"})
	cancel := startRunner(t, r)
	defer cancel()
	deadline := time.After(5 * time.Second)
	for {
		if len(r.Active()) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for active jobs")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if len(r.Active()) == 0 {
		t.Error("Active returned nothing while jobs are running/queued")
	}
	close(hold)
	waitJob(t, r, "q")
	if _, ok := r.Last("q"); !ok {
		t.Error("Last returned false after job completed")
	}
}

func TestWaitErrorRequeuesWithoutCountingAttempt(t *testing.T) {
	d := openDB(t)
	cl := newClock(base)
	var runs atomic.Int32
	started := make(chan struct{}, 1)
	r := newRunner(d, nil, cl)
	retryAt := base.Add(10 * time.Minute)
	r.Register(runner.Kind{Name: "w", Resume: true, Run: func(_ context.Context, _ *runner.Job) error {
		n := runs.Add(1)
		if n == 1 {
			started <- struct{}{}
			return runner.WaitUntil(retryAt)
		}
		return nil
	}})
	id, _ := r.Enqueue(runner.Spec{Kind: "w", Subject: "1"})
	cancel := startRunner(t, r)
	defer cancel()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("job never started")
	}
	deadline := time.After(5 * time.Second)
	for {
		var nb string
		var attempts int
		d.QueryRow(`SELECT not_before, attempts FROM job WHERE id=?`, id).Scan(&nb, &attempts)
		want := retryAt.UTC().Format(time.RFC3339)
		if nb == want && attempts == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("not_before=%q attempts=%d, want not_before=%q attempts=0", nb, attempts, want)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cl.Set(retryAt.Add(time.Second))
	waitJob(t, r, "w")
	if int(runs.Load()) != 2 {
		t.Errorf("runs: got %d, want 2", runs.Load())
	}
}

func TestWaitErrorMovesOtherQueuedJobsOfSameKind(t *testing.T) {
	d := openDB(t)
	cl := newClock(base)
	triggerRunning := make(chan struct{}, 1)
	hold := make(chan struct{})
	r := newRunner(d, nil, cl)
	retryAt := base.Add(10 * time.Minute)
	r.Register(runner.Kind{Name: "wk", Serial: true, Resume: true, Run: func(_ context.Context, j *runner.Job) error {
		if j.Subject == "trigger" {
			triggerRunning <- struct{}{}
			<-hold
			return runner.WaitUntil(retryAt)
		}
		return nil
	}})
	id1, _ := r.Enqueue(runner.Spec{Kind: "wk", Subject: "trigger"})
	id2, _ := r.Enqueue(runner.Spec{Kind: "wk", Subject: "other"})
	cancel := startRunner(t, r)
	defer cancel()
	select {
	case <-triggerRunning:
	case <-time.After(5 * time.Second):
		t.Fatal("trigger job did not start running")
	}
	close(hold)
	deadline := time.After(5 * time.Second)
	want := retryAt.UTC().Format(time.RFC3339)
	for {
		var nb1, nb2 string
		d.QueryRow(`SELECT not_before FROM job WHERE id=?`, id1).Scan(&nb1)
		d.QueryRow(`SELECT not_before FROM job WHERE id=?`, id2).Scan(&nb2)
		if nb1 == want && nb2 == want {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("jobs not moved to wait time: trigger=%q other=%q want=%q", nb1, nb2, want)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestSerialKindRunsOneAtATime(t *testing.T) {
	d := openDB(t)
	var running atomic.Int32
	var maxRunning atomic.Int32
	hold := make(chan struct{})
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "serial", Serial: true, Run: func(ctx context.Context, _ *runner.Job) error {
		cur := running.Add(1)
		for {
			old := maxRunning.Load()
			if cur <= old || maxRunning.CompareAndSwap(old, cur) {
				break
			}
		}
		<-hold
		running.Add(-1)
		return nil
	}})
	r.Enqueue(runner.Spec{Kind: "serial", Subject: "1"})
	r.Enqueue(runner.Spec{Kind: "serial", Subject: "2"})
	r.Enqueue(runner.Spec{Kind: "serial", Subject: "3"})
	cancel := startRunner(t, r)
	defer cancel()
	deadline := time.After(5 * time.Second)
	for {
		if running.Load() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("serial jobs never started")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if maxRunning.Load() > 1 {
		t.Errorf("serial kind ran %d jobs at once, want max 1", maxRunning.Load())
	}
	close(hold)
}

func TestActiveByKindFiltersResults(t *testing.T) {
	d := openDB(t)
	hold := make(chan struct{})
	r := newRunner(d, nil, nil)
	r.Register(runner.Kind{Name: "aa", Run: func(ctx context.Context, _ *runner.Job) error { <-hold; return nil }})
	r.Register(runner.Kind{Name: "bb", Run: func(ctx context.Context, _ *runner.Job) error { <-hold; return nil }})
	r.Enqueue(runner.Spec{Kind: "aa", Subject: "1"})
	r.Enqueue(runner.Spec{Kind: "bb", Subject: "1"})
	cancel := startRunner(t, r)
	defer cancel()
	deadline := time.After(5 * time.Second)
	for {
		if len(r.Active()) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for both jobs to be active")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	aa := r.Active("aa")
	bb := r.Active("bb")
	if len(aa) != 1 || aa[0].Kind != "aa" {
		t.Errorf("Active(aa): got %v", aa)
	}
	if len(bb) != 1 || bb[0].Kind != "bb" {
		t.Errorf("Active(bb): got %v", bb)
	}
	close(hold)
}
