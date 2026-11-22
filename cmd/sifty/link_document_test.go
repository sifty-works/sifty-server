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
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/fetch/fetchtest"
	"github.com/sifty-works/sifty-server/internal/models"
	"github.com/sifty-works/sifty-server/internal/runner"
)

func TestRefreshWithNewTextStartsAnalysisAgainFromItsFirstChunk(t *testing.T) {
	const address = "https://example.com/about"
	page := func(sentence string, size int) fetchtest.MapEntry {
		text := strings.TrimSpace(strings.Repeat(sentence+" ", size/len(sentence)))
		return fetchtest.MapEntry{Response: fetch.Response{URL: address, Status: 200, ContentType: "text/html", Body: []byte("<p>" + text + "</p>")}}
	}
	pages := fetchtest.NewMap()
	pages.Set(address, page("Old note about distributed systems.", 40000))
	d := openTestDB(t)
	fm := &models.FakeModel{}
	for range 40 {
		fm.AddReply(models.Response{Text: emptyProposalReply()})
	}
	m := &pauseOnCall4{FakeModel: fm, blocked: make(chan struct{})}

	r1 := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r1, d, m)
	base1 := newRouteServerWithReaders(t, d, r1, candidate.Readers{Pages: pages})
	doc, _ := apiSend(t, http.MethodPost, base1, "/api/documents", map[string]any{"url": address})
	path := fmt.Sprintf("/api/documents/%.0f", doc["id"])
	apiSend(t, http.MethodPost, base1, path+"/analyze", nil)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	go r1.Start(ctx1)
	select {
	case <-m.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("analysis did not reach its fourth chunk")
	}
	cancel1()

	r2 := runner.New(d, nil, time.Now, slog.Default())
	registerKindsWith(t, r2, d, m)
	base2 := newRouteServerWithReaders(t, d, r2, candidate.Readers{Pages: pages})
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go r2.Start(ctx2)
	waitFor(t, 5*time.Second, func() bool {
		var state string
		d.QueryRow(`SELECT state FROM job WHERE kind='analyze_document'`).Scan(&state)
		return state == "interrupted"
	})
	if _, err := candidate.ResetInterruptedAnalyses(ctx2, d); err != nil {
		t.Fatalf("reset interrupted analyses: %v", err)
	}
	pages.Set(address, page("New note about compilers.", 20000))
	refreshed, status := apiSend(t, http.MethodPost, base2, path+"/refresh", nil)
	if status != http.StatusOK || refreshed["state"] != "ready" {
		t.Fatalf("refresh: status %d, body %v", status, refreshed)
	}
	apiSend(t, http.MethodPost, base2, path+"/analyze", nil)
	waitForDocState(t, base2, doc["id"].(float64), "analysed", 10*time.Second)

	newChunks := candidate.SplitChunks(strings.TrimSpace(strings.Repeat("New note about compilers. ", 20000/len("New note about compilers."))))
	calls := fm.Calls()
	if len(calls) != 3+len(newChunks) {
		t.Fatalf("model calls = %d, want 3 before the refresh and %d after it", len(calls), len(newChunks))
	}
	if !strings.Contains(calls[3].Prompt, newChunks[0]) {
		t.Errorf("the first call after the refresh did not read the first chunk of the new text")
	}
}
