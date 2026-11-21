package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sifty-works/sifty-server/internal/candidate"
	"github.com/sifty-works/sifty-server/internal/db"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

type cancelledRun struct {
	d      *db.DB
	fm     *models.FakeModel
	m      *pauseOnCall4
	base   string
	path   string
	docID  float64
	chunks int
	stop   context.CancelFunc
}

func jobState(d *db.DB) string {
	var state string
	d.QueryRow(`SELECT state FROM job WHERE kind='analyze_document'`).Scan(&state)
	return state
}

func cancelAnalysisAtFourthChunk(t *testing.T) cancelledRun {
	t.Helper()
	text := strings.Repeat("Experienced software engineer with strong distributed systems background. ", 600)
	chunks := len(candidate.SplitChunks(text))
	if chunks < 4 {
		t.Fatalf("need at least 4 chunks, got %d", chunks)
	}
	fm := &models.FakeModel{}
	for i := 0; i < chunks; i++ {
		fm.AddReply(models.Response{Text: emptyProposalReply()})
	}
	m := &pauseOnCall4{FakeModel: fm, blocked: make(chan struct{})}
	d := openTestDB(t)
	r := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r, d, m)
	base := newRouteServer(t, d, r)
	doc, _ := pasteAnalyzeDoc(t, base, "cv.txt", text)
	path := fmt.Sprintf("/api/documents/%.0f/", doc["id"])
	apiSend(t, http.MethodPost, base, path+"analyze", nil)
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go r.Start(ctx)
	select {
	case <-m.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not reach the fourth chunk")
	}
	out, status := apiSend(t, http.MethodPost, base, path+"cancel", nil)
	if status != http.StatusOK || out["state"] != "ready" || out["stateReason"] != "Analysis cancelled." {
		t.Fatalf("cancel answered %d %v, want 200 ready with the reason", status, out)
	}
	return cancelledRun{d, fm, m, base, path, doc["id"].(float64), chunks, stop}
}

func TestAnalyzeAfterCancelContinuesFromTheChunkReached(t *testing.T) {
	c := cancelAnalysisAtFourthChunk(t)
	waitFor(t, 5*time.Second, c.m.done.Load)
	apiSend(t, http.MethodPost, c.base, c.path+"analyze", nil)
	waitForDocState(t, c.base, c.docID, "analysed", 10*time.Second)
	if n := len(c.fm.Calls()); n != c.chunks {
		t.Errorf("model calls = %d, want %d (the three chunks read before the cancel are not read again)", n, c.chunks)
	}
}

func TestCancelledAnalysisIsNotResumedByARestart(t *testing.T) {
	c := cancelAnalysisAtFourthChunk(t)
	waitFor(t, 5*time.Second, c.m.done.Load)
	c.stop()
	r := runner.New(c.d, nil, time.Now, slog.Default())
	registerKindsWith(t, r, c.d, c.m)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go r.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	if n := len(c.fm.Calls()); n != 3 {
		t.Errorf("model calls = %d, want 3 (a cancelled analysis does not restart)", n)
	}
	if got := jobState(c.d); got != "cancelled" {
		t.Errorf("job state after restart = %q, want cancelled", got)
	}
}

func TestCancelQueuedAnalysisNeverCallsTheModel(t *testing.T) {
	fix := newAnalyzeFix(t)
	doc, _ := pasteAnalyzeDoc(t, fix.base, "cv.txt", "Expert Go developer seeking backend roles.")
	path := fmt.Sprintf("/api/documents/%.0f/", doc["id"])
	apiSend(t, http.MethodPost, fix.base, path+"analyze", nil)
	out, status := apiSend(t, http.MethodPost, fix.base, path+"cancel", nil)
	if status != http.StatusOK || out["state"] != "ready" || out["stateReason"] != "Analysis cancelled." {
		t.Fatalf("cancel answered %d %v, want 200 ready with the reason", status, out)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go fix.r.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	if n := len(fix.fm.Calls()); n != 0 {
		t.Errorf("model calls = %d, want 0", n)
	}
	if got := jobState(fix.d); got != "cancelled" {
		t.Errorf("job state = %q, want cancelled", got)
	}
}

func TestCancelOfADocumentThatIsNotAnalysingChangesNothing(t *testing.T) {
	fix := newAnalyzeFix(t)
	doc, _ := pasteAnalyzeDoc(t, fix.base, "cv.txt", "Expert Go developer seeking backend roles.")
	out, status := apiSend(t, http.MethodPost, fix.base, fmt.Sprintf("/api/documents/%.0f/cancel", doc["id"]), nil)
	if status != http.StatusConflict || out["error"] != "This document is not being analysed." {
		t.Errorf("cancel answered %d %v, want 409 saying the document is not being analysed", status, out)
	}
}
