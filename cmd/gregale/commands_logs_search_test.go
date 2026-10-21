package main_test

// adr: 20261018083536 — `gregale logs search|facets` send the filters and render the ledger.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func logSearchServer(t *testing.T, payload any) (*string, *url.Values) {
	t.Helper()
	var gotPath string
	gotQuery := url.Values{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	return &gotPath, &gotQuery
}

func captureLogSearchStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	return &stdout
}

func TestCmdLogsSearch_SendsFiltersAndRendersEvents(t *testing.T) {
	at := time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC)
	gotPath, gotQuery := logSearchServer(t, api.LogEventsResponse{
		Since: at.Add(-time.Hour), Until: at, NextCursor: "abc",
		Events: []api.LogQueryEvent{
			{Timestamp: "2026-10-18T10:59:00Z", Source: api.LogSourceHTTP, Status: 502, Method: "POST", Route: "/checkout", Message: "POST /checkout returned 502 in 31ms", Count: 3},
			{Timestamp: "2026-10-18T10:58:00Z", Source: api.LogSourceRuntime, Level: "error", Message: "db timeout"},
		},
	})
	stdout := captureLogSearchStdout(t)
	if code := main.CmdLogs([]string{"search", "shop", "--q", "timeout", "--status", "5xx", "--level", "error,warn", "--since", "6h", "--limit", "50"}); code != 0 {
		t.Fatalf("logs search = %d", code)
	}
	if *gotPath != "/v1/apps/shop/log-events" {
		t.Fatalf("path = %s", *gotPath)
	}
	for key, want := range map[string]string{"q": "timeout", "status": "5xx", "level": "error,warn", "since": "6h", "limit": "50"} {
		if got := gotQuery.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	out := stdout.String()
	for _, want := range []string{"502 POST /checkout", "(x3)", "error", "db timeout", "--cursor abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestCmdLogsFacets_RendersCounts(t *testing.T) {
	at := time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC)
	gotPath, gotQuery := logSearchServer(t, api.LogEventFacetsResponse{
		Since: at.Add(-24 * time.Hour), Until: at, Total: 12,
		Sources:       []api.LogFacetCount{{Value: "http", Count: 10}, {Value: "runtime", Count: 2}},
		StatusClasses: []api.LogFacetCount{{Value: "2xx", Count: 8}, {Value: "5xx", Count: 2}},
		Routes:        []api.LogFacetCount{{Value: "/checkout", Count: 7}}, RoutesTruncated: true,
	})
	stdout := captureLogSearchStdout(t)
	if code := main.CmdLogs([]string{"facets", "shop", "--since", "24h", "--source", "http"}); code != 0 {
		t.Fatalf("logs facets = %d", code)
	}
	if *gotPath != "/v1/apps/shop/log-events/facets" || gotQuery.Get("source") != "http" || gotQuery.Get("limit") != "" {
		t.Fatalf("path %s query %v", *gotPath, *gotQuery)
	}
	out := stdout.String()
	for _, want := range []string{"shop: 12 events", "Sources: http 10, runtime 2", "Status:  2xx 8, 5xx 2", "Routes:  /checkout 7", "busiest routes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}
