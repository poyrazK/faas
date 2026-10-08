// commands_metrics_test.go — Move 1 PR-A: tests for the new
// `gregale metrics <slug> [--range 5m]` command. Mirrors the
// commands_deployments_test.go shape: httptest fake-apid that
// records the request and returns the supplied payload, plus
// t.Setenv for FAAS_API / FAAS_TOKEN, plus the osStdout seam for
// human-mode assertions.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// metricsHandler returns a fake-apid that records the request path +
// the range query, then responds with the supplied metrics payload.
// Used by the happy-path tests below; error tests substitute their
// own handler.
func metricsHandler(t *testing.T, payload api.AppMetricsResponse) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(payload)
	}))
	return srv, &gotPath, &gotQuery
}

// --- happy paths ------------------------------------------------------------

func TestCmdMetrics_HappyPath_DefaultRange(t *testing.T) {
	srv, gotPath, gotQuery := metricsHandler(t, api.AppMetricsResponse{
		AppID: "abc-123", Range: "5m", Source: "prometheus",
		RequestCount: 42,
		LatencyP50MS: 12.5, LatencyP95MS: 88.0, LatencyP99MS: 240.0,
		ErrorRatePct: 0.12, ColdStartPct: 8.4, WakeP95MS: 320.0,
	})
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdMetrics([]string{"abc-123"}); code != 0 {
		t.Fatalf("metrics = %d, want 0", code)
	}
	if *gotPath != "/v1/apps/abc-123/metrics" {
		t.Errorf("path = %q, want /v1/apps/abc-123/metrics", *gotPath)
	}
	if *gotQuery != "range=5m" {
		t.Errorf("query = %q, want range=5m (default range)", *gotQuery)
	}
	out := stdout.String()
	for _, want := range []string{
		"App:", "abc-123",
		"Range:", "5m",
		"Source:", "prometheus",
		"Requests:", "42",
		"Latency:", "p50=12.5ms", "p95=88.0ms", "p99=240.0ms",
		"Error rate:", "0.12%",
		"Cold boot:", "8.40%",
		"Wake p95:", "320ms",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestCmdMetrics_HappyPath_ExplicitRange(t *testing.T) {
	srv, gotPath, gotQuery := metricsHandler(t, api.AppMetricsResponse{
		AppID: "abc-123", Range: "1h", Source: "prometheus",
		RequestCount: 1024,
	})
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdMetrics([]string{"--range", "1h", "abc-123"}); code != 0 {
		t.Fatalf("metrics --range 1h = %d, want 0", code)
	}
	if *gotPath != "/v1/apps/abc-123/metrics" {
		t.Errorf("path = %q, want /v1/apps/abc-123/metrics", *gotPath)
	}
	if *gotQuery != "range=1h" {
		t.Errorf("query = %q, want range=1h", *gotQuery)
	}
}

// --- degraded source (Prometheus unreachable) -------------------------------

// TestCmdMetrics_DegradedSourceWarnsOnZeroes: when the apid reports
// `source: degraded: <reason>` (Prometheus is down), the human-mode
// line emits a one-line note before the values so the customer
// understands the zeroes aren't a bug.
func TestCmdMetrics_DegradedSourceWarnsOnZeroes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppMetricsResponse{
			AppID: "abc-123", Range: "5m",
			Source: "degraded: prometheus unreachable",
			// every numeric field zero by default
		})
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdMetrics([]string{"abc-123"}); code != 0 {
		t.Fatalf("metrics = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "degraded: prometheus unreachable") {
		t.Errorf("output missing degraded-source warning\nfull: %s", stdout.String())
	}
}

// --- client-side rejections -------------------------------------------------

func TestCmdMetrics_NoPositional(t *testing.T) {
	_, readStderr, restore := swapIO(t)
	defer restore()
	if code := cmdMetrics(nil); code != 1 {
		t.Errorf("metrics bare = %d, want 1", code)
	}
	if !strings.Contains(readStderr(), "usage: gregale metrics") {
		t.Errorf("stderr missing usage line\nfull: %s", readStderr())
	}
}

func TestCmdMetrics_TooManyPositionals(t *testing.T) {
	_, readStderr, restore := swapIO(t)
	defer restore()
	if code := cmdMetrics([]string{"a", "b"}); code != 1 {
		t.Errorf("metrics a b = %d, want 1", code)
	}
	if !strings.Contains(readStderr(), "usage: gregale metrics") {
		t.Errorf("stderr missing usage line\nfull: %s", readStderr())
	}
}

func TestCmdMetrics_Unauthenticated(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if code := cmdMetrics([]string{"abc-123"}); code == 0 {
		t.Error("metrics without token must fail")
	}
}

// --- JSON output ------------------------------------------------------------

func TestCmdMetrics_JSON_SingleRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppMetricsResponse{
			AppID: "abc-123", Range: "5m", Source: "prometheus",
			RequestCount: 42,
		})
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdMetrics([]string{"abc-123"}); code != 0 {
		t.Fatalf("metrics json = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "\n  ") {
		t.Fatalf("expected indented JSON, got %q", out)
	}
	var m api.AppMetricsResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if m.AppID != "abc-123" || m.Range != "5m" || m.RequestCount != 42 {
		t.Errorf("JSON shape drift on AppMetricsResponse; got %+v", m)
	}
}

// --- dispatcher (back-compat) ----------------------------------------------

// TestRun_DispatchMetrics pins the main run() switch routes
// `metrics` into cmdMetrics rather than mis-routing to the default
// unknown-command branch.
func TestRun_DispatchMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/abc-123/metrics":
			_ = json.NewEncoder(w).Encode(api.AppMetricsResponse{AppID: "abc-123", Range: "5m"})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := run([]string{"metrics", "abc-123"}); code != 0 {
		t.Errorf("run metrics = %d, want 0", code)
	}
}

// --- service map (ADR-732) ---------------------------------------------------

func serviceMapServer(t *testing.T, payload api.ServiceMapResponse) (*string, *string) {
	t.Helper()
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	return &gotPath, &gotQuery
}

func captureMetricsStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })
	return &stdout
}

func TestCmdMetrics_Services_OmittedRangeDefersToServer(t *testing.T) {
	gotPath, gotQuery := serviceMapServer(t, api.ServiceMapResponse{
		Range: "1h", Source: "prometheus", Truncated: true,
		Edges: []api.ServiceMapEdge{{
			CallerAppSlug: "public-api", TargetAppSlug: "billing",
			Calls: 12040, Errors: 48, ErrorRatePct: 0.4, LatencyP50MS: 11.2, LatencyP95MS: 38,
		}},
	})
	stdout := captureMetricsStdout(t)

	if code := cmdMetrics([]string{"--services"}); code != 0 {
		t.Fatalf("metrics --services = %d, want 0", code)
	}
	if *gotPath != "/v1/service-map" || *gotQuery != "" {
		t.Fatalf("request = %q?%q, want /v1/service-map with no range", *gotPath, *gotQuery)
	}
	out := stdout.String()
	for _, want := range []string{
		"Range:      1h",
		"public-api → billing",
		"Calls:      12040 (errors 48, 0.40%)",
		"p50=11.2ms p95=38.0ms",
		"showing the 1 busiest edges",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestCmdMetrics_Services_ExplicitRange(t *testing.T) {
	_, gotQuery := serviceMapServer(t, api.ServiceMapResponse{Range: "24h", Source: "prometheus"})
	stdout := captureMetricsStdout(t)

	if code := cmdMetrics([]string{"--services", "--range", "24h"}); code != 0 {
		t.Fatalf("metrics --services --range 24h = %d, want 0", code)
	}
	if *gotQuery != "range=24h" {
		t.Fatalf("query = %q, want range=24h", *gotQuery)
	}
	if !strings.Contains(stdout.String(), "(no service-to-service calls in window)") {
		t.Fatalf("empty map should say so\nfull: %s", stdout.String())
	}
}

func TestCmdMetrics_Services_DegradedSourceWarns(t *testing.T) {
	serviceMapServer(t, api.ServiceMapResponse{Range: "1h", Source: "degraded: telemetry timeout"})
	stdout := captureMetricsStdout(t)

	if code := cmdMetrics([]string{"--services"}); code != 0 {
		t.Fatalf("metrics --services = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Prometheus is unavailable") {
		t.Fatalf("degraded map should warn\nfull: %s", stdout.String())
	}
}

func TestCmdMetrics_Services_RejectsAccountAndSlug(t *testing.T) {
	for _, args := range [][]string{
		{"--services", "--account"},
		{"--services", "abc-123"},
		{"--services", "--app", "abc-123"},
	} {
		_, readStderr, restore := swapIO(t)
		if code := cmdMetrics(args); code != 1 {
			t.Errorf("metrics %v = %d, want 1", args, code)
		}
		if !strings.Contains(readStderr(), "--services") {
			t.Errorf("metrics %v: usage should mention --services\nfull: %s", args, readStderr())
		}
		restore()
	}
}

// --- helper coverage --------------------------------------------------------

// silenceUnusedIOImport: keep the io import meaningful so the test
// file compiles even if a future refactor drops swapIO in favour of
// the package-level seam. Belt-and-braces for editor-driven cleanups.
var _ = io.Discard
