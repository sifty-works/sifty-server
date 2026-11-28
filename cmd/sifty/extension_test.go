package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sifty-works/sifty-server/extension"
)

func TestExtensionOriginOnlyOnExtensionRoutes(t *testing.T) {
	port, cancel := startServer(t, t.TempDir(), true)
	defer cancel()
	origin, err := extension.Origin()
	if err != nil {
		t.Fatal(err)
	}
	local := fmt.Sprintf("http://127.0.0.1:%d", port)

	send := func(method, path, from string) int {
		t.Helper()
		req, _ := http.NewRequest(method, local+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		if from != "" {
			req.Header.Set("Origin", from)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := send(http.MethodPost, "/api/extension/fill", origin); got == http.StatusForbidden {
		t.Errorf("extension origin on /api/extension/fill: got %d, want it accepted", got)
	}
	for _, path := range []string{"/api/status", "/api/extension/../status", "/api/extension/%2e%2e/status"} {
		if got := send(http.MethodGet, path, origin); got != http.StatusForbidden {
			t.Errorf("extension origin on GET %s: got %d, want 403", path, got)
		}
	}
	if got := send(http.MethodPatch, "/api/models", origin); got != http.StatusForbidden {
		t.Errorf("extension origin on PATCH /api/models: got %d, want 403", got)
	}
	if got := send(http.MethodPost, "/api/extension/fill", "chrome-extension://"+strings.Repeat("a", 32)); got != http.StatusForbidden {
		t.Errorf("another extension's origin on /api/extension/fill: got %d, want 403", got)
	}
	if got := send(http.MethodGet, "/api/status", local); got != http.StatusOK {
		t.Errorf("local origin on /api/status: got %d, want 200", got)
	}
}
