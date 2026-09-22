package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNormalizeLogsSince(t *testing.T) {
	now := time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, raw, source, want string
		wantErr                 bool
	}{
		{name: "runtime duration", raw: "15m", source: logsSourceRuntime, want: "2026-09-22T11:45:00Z"},
		{name: "runtime day alias", raw: "3d", source: logsSourceRuntime, want: "2026-09-19T12:00:00Z"},
		{name: "runtime timestamp", raw: "2026-09-21T12:00:00+03:00", source: logsSourceRuntime, want: "2026-09-21T09:00:00Z"},
		{name: "http duration", raw: "15m", source: logsSourceHTTP, want: "15m"},
		{name: "http timestamp", raw: "2026-09-22T11:45:00Z", source: logsSourceHTTP, want: "15m0s"},
		{name: "invalid", raw: "yesterday", source: logsSourceRuntime, wantErr: true},
		{name: "future", raw: "2026-09-22T12:01:00Z", source: logsSourceHTTP, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeLogsSince(tt.raw, tt.source, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeLogsSince() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("normalizeLogsSince() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCmdLogsHTTPFiltersUseRequestDatabase(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/myapp/debug/requests" {
			t.Errorf("path = %q, want request telemetry endpoint", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"since":"15m","complete":true,"requests":[{"id":"row-1","deployment_id":"dep-1","route":"/checkout","method":"POST","status":500,"latency_ms":123,"count":2,"cold_boot":true,"trace_id":"req_28372","received_at":"2026-09-22T11:59:00Z"}]}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdLogs([]string{"myapp", "--status", "500", "--route", "/checkout", "--since", "15m"}); code != 0 {
		t.Fatalf("cmdLogs exit = %d", code)
	}
	for key, want := range map[string]string{
		"status": "500", "route": "/checkout", "since": "15m", "limit": "100",
	} {
		if got := gotQuery.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	for _, want := range []string{"http", "status=500", `route="/checkout"`, "request=req_28372", "count=2", "cold_boot=true"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output %q missing %q", stdout.String(), want)
		}
	}
}

func TestCmdLogsRelativeSinceReachesRuntimeAsTimestamp(t *testing.T) {
	var gotSince string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/myapp/logs" {
			t.Errorf("path = %q, want runtime log endpoint", r.URL.Path)
		}
		gotSince = r.URL.Query().Get("since")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	started := time.Now()
	if code := cmdLogs([]string{"myapp", "--since", "15m"}); code != 0 {
		t.Fatalf("cmdLogs exit = %d", code)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, gotSince)
	if err != nil {
		t.Fatalf("runtime since = %q, want RFC3339 timestamp: %v", gotSince, err)
	}
	want := started.Add(-15 * time.Minute)
	if delta := timestamp.Sub(want); delta < 0 || delta > 5*time.Second {
		t.Errorf("runtime since = %s, want approximately %s (delta %s)", timestamp, want, delta)
	}
}

func TestCmdLogsRequestLookupEmitsCanonicalJSON(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"row-1","deployment_id":"dep-1","route":"/checkout","method":"POST","status":500,"latency_ms":123,"count":1,"cold_boot":false,"trace_id":"req_28372","received_at":"2026-09-22T11:59:00Z"}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdLogs([]string{"--request", "req_28372", "myapp"}); code != 0 {
		t.Fatalf("cmdLogs exit = %d", code)
	}
	if gotPath != "/v1/apps/myapp/debug/requests/req_28372" {
		t.Fatalf("path = %q", gotPath)
	}
	var event api.LogQueryEvent
	if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
		t.Fatalf("decode NDJSON event: %v; output=%q", err, stdout.String())
	}
	if event.Source != api.LogSourceHTTP || event.RequestID != "req_28372" || event.Status != 500 || event.Route != "/checkout" {
		t.Fatalf("event = %+v", event)
	}
}

func TestCmdLogsReleaseRevisionFiltersHTTPLogs(t *testing.T) {
	const deploymentID = "11111111-1111-4111-8111-111111111111"
	var telemetryQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/myapp/deployments":
			_, _ = fmt.Fprintf(w, `{"items":[{"id":%q,"revision":384}]}`, deploymentID)
		case "/v1/apps/myapp/debug/requests":
			telemetryQuery = r.URL.Query()
			_, _ = fmt.Fprint(w, `{"since":"24h","complete":true,"requests":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	if code := cmdLogs([]string{"myapp", "--source", "http", "--release", "384"}); code != 0 {
		t.Fatalf("cmdLogs exit = %d", code)
	}
	if got := telemetryQuery.Get("deployment_id"); got != deploymentID {
		t.Errorf("deployment_id = %q, want %q", got, deploymentID)
	}
}

func TestCmdLogsAllWalksEveryHTTPLogPage(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if got := r.URL.Query().Get("limit"); got != "1" {
			t.Errorf("limit = %q, want 1", got)
		}
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = fmt.Fprint(w, `{"since":"15m","complete":false,"next_cursor":"page-2","requests":[{"id":"row-1","route":"/one","method":"GET","status":200,"latency_ms":1,"count":1,"received_at":"2026-09-22T11:59:00Z"}]}`)
		case "page-2":
			_, _ = fmt.Fprint(w, `{"since":"15m","complete":true,"requests":[{"id":"row-2","route":"/two","method":"GET","status":201,"latency_ms":2,"count":1,"received_at":"2026-09-22T11:58:00Z"}]}`)
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdLogs([]string{"myapp", "--source", "http", "--since", "15m", "--limit", "1", "--all"}); code != 0 {
		t.Fatalf("cmdLogs exit = %d", code)
	}
	if calls != 2 {
		t.Fatalf("HTTP calls = %d, want 2", calls)
	}
	for _, want := range []string{`route="/one"`, `route="/two"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output %q missing %q", stdout.String(), want)
		}
	}
}

func TestCmdLogsAllStreamsCompletedPagesBeforeLaterPageFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") != "" {
			http.Error(w, `{"code":"internal"}`, http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, `{"complete":false,"next_cursor":"page-2","requests":[{"id":"row-1","route":"/first","method":"GET","status":200,"latency_ms":1,"count":1,"received_at":"2026-09-22T11:59:00Z"}]}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdLogs([]string{"myapp", "--source", "http", "--all"}); code == 0 {
		t.Fatal("expected later-page failure")
	}
	if !strings.Contains(stdout.String(), `route="/first"`) {
		t.Fatalf("first page was buffered until after the failing page: %q", stdout.String())
	}
}

func TestHTTPLogQueryPageWarnings(t *testing.T) {
	var output bytes.Buffer
	renderHTTPLogQueryPageWarnings(&output, api.DebugTelemetryListResponse{
		RetentionClamped: true,
		Complete:         false,
	})
	for _, want := range []string{"clamped", "--all"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("warning output %q missing %q", output.String(), want)
		}
	}
}

func TestCmdLogsRejectsInvalidHTTPFiltersBeforeNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	for _, args := range [][]string{
		{"myapp", "--status", "99"},
		{"myapp", "--source", "runtime", "--route", "/checkout"},
		{"myapp", "--source", "http", "--follow"},
		{"myapp", "--request", "req_1", "--all"},
		{"myapp", "--source", "database"},
	} {
		if code := cmdLogs(args); code != 2 {
			t.Errorf("cmdLogs(%v) = %d, want 2", args, code)
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("invalid filters made %d network request(s)", got)
	}
}
