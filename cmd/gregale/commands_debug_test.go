package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDebugRequestsList_SendsFiltersToServer(t *testing.T) {
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugTelemetryListResponse{
			Since: "6h",
			Requests: []api.DebugTelemetryRequestItem{{
				ID: "request-1", Route: "GET /checkout", Method: "GET", Status: 200,
				LatencyMS: 42, Count: 7, ReceivedAt: "2026-09-06T10:00:00Z",
			}},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	// The slug intentionally appears before the flags. This is the form
	// shown in the command's top-level docs and must not drop the filters.
	if code := cmdDebugRequestsList([]string{
		"my-app", "--since", "6h", "--route", "GET /checkout", "--limit", "50",
	}); code != 0 {
		t.Fatalf("cmdDebugRequestsList() = %d, want 0", code)
	}

	if got.URL.Path != "/v1/apps/my-app/debug/requests" {
		t.Fatalf("request path = %q, want /v1/apps/my-app/debug/requests", got.URL.Path)
	}
	q := got.URL.Query()
	for key, want := range map[string]string{
		"since": "6h", "route": "GET /checkout", "limit": "50",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(stdout.String(), "COUNT") || !strings.Contains(stdout.String(), "7") {
		t.Errorf("human output does not show collapsed request count:\n%s", stdout.String())
	}
}

func TestCmdDebugRequestsList_RejectsInvalidLimitBeforeNetwork(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_test")
	_, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdDebugRequestsList([]string{"my-app", "--limit", "201"}); code != 1 {
		t.Fatalf("cmdDebugRequestsList() = %d, want 1", code)
	}
	if got := readStderr(); !strings.Contains(got, "--limit must be between 1 and 200") {
		t.Errorf("stderr = %q, want limit validation", got)
	}
}

func TestCmdDebugHelp(t *testing.T) {
	_, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdDebug([]string{"--help"}); code != 0 {
		t.Fatalf("cmdDebug(--help) = %d, want 0", code)
	}
	got := readStderr()
	for _, want := range []string{"usage: gregale debug", "requests list", "regressions", "compare"} {
		if !strings.Contains(got, want) {
			t.Errorf("help missing %q:\n%s", want, got)
		}
	}
}
