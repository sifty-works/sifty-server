package main

import (
	"net/http"
	"testing"
	"time"
)

func waitingReason(t *testing.T, base, kind string) string {
	t.Helper()
	for _, w := range getWork(t, base).Waiting {
		if w.Kind == kind {
			if w.Reason == nil {
				return "none"
			}
			return w.Reason.Code
		}
	}
	return "absent"
}

func TestPausedInstanceHoldsQueuedWorkWhileCollectNowStillRuns(t *testing.T) {
	base, _ := startModelsServe(t)
	waitFor(t, 5*time.Second, func() bool {
		w := getWork(t, base)
		return len(w.Running) == 0 && len(w.Waiting) == 0
	})

	apiSend(t, http.MethodPatch, base, "/api/models", `{"paused":true}`)
	apiSend(t, http.MethodPatch, base, "/api/models", `{"allowPaidBulk":true}`)
	if got := waitingReason(t, base, "recompute_fit"); got != "paused" {
		t.Fatalf("queued recompute_fit while paused: reason %q, want paused", got)
	}

	if _, status := apiSend(t, http.MethodPost, base, "/api/collect", `{"force":true}`); status != http.StatusOK {
		t.Fatalf("POST /api/collect: status %d", status)
	}
	waitFor(t, 5*time.Second, func() bool {
		body, _ := apiGet(t, base, "/api/status")
		last, _ := body["lastRun"].(map[string]any)
		return last != nil && last["state"] == "done"
	})
	time.Sleep(1500 * time.Millisecond)
	if got := waitingReason(t, base, "recompute_fit"); got != "paused" {
		t.Errorf("recompute_fit after Collect now while paused: reason %q, want it still held", got)
	}

	apiSend(t, http.MethodPatch, base, "/api/models", `{"paused":false}`)
	waitFor(t, 5*time.Second, func() bool {
		w := getWork(t, base)
		return len(w.Running) == 0 && len(w.Waiting) == 0
	})
}
