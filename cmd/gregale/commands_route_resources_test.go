package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func routeResourcesStub(t *testing.T, gotSince *string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/analytics/route-resources" {
			http.NotFound(w, r)
			return
		}
		*gotSince = r.URL.Query().Get("since")
		_ = json.NewEncoder(w).Encode(api.RouteResourcesResponse{Slug: "demo", From: "2026-11-21T00:00:00Z", Until: "2026-11-22T00:00:00Z"})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
}

// `gregale metric resources --json` prints the API response as one JSON
// document, not the route table (ADR-20261121170000).
func TestRouteResourcesJSON(t *testing.T) {
	var since string
	routeResourcesStub(t, &since)
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })

	if code := cmdRouteResources([]string{"demo", "--since", "2h"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if since != "2h" {
		t.Errorf("since = %q, want 2h", since)
	}
	var doc api.RouteResourcesResponse
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout.String())
	}
	if doc.Slug != "demo" {
		t.Errorf("slug = %q, want demo", doc.Slug)
	}
}

func TestRouteResourcesHuman(t *testing.T) {
	var since string
	routeResourcesStub(t, &since)
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdRouteResources([]string{"demo"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if since != "24h" {
		t.Errorf("since = %q, want the 24h default", since)
	}
	if got := stdout.String(); !strings.Contains(got, "App:     demo") || !strings.Contains(got, "No route activity") {
		t.Errorf("human output:\n%s", got)
	}
}
