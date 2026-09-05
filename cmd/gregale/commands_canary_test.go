package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/api/canary"
)

func TestCmdCanarySimulate_HappyPath(t *testing.T) {
	var gotPath, gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRange = r.URL.Query().Get("range")
		_ = json.NewEncoder(w).Encode(api.AppMetricsResponse{
			AppID: "app-123", Range: "1h", Source: "prometheus",
			RequestCount: 3600, ErrorRatePct: 1.25, LatencyP95MS: 42.5,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdCanary([]string{"simulate", "my-app", "--canary-preset", "slow"}); code != 0 {
		t.Fatalf("canary simulate = %d, want 0", code)
	}
	if gotPath != "/v1/apps/my-app/metrics" {
		t.Errorf("path = %q, want /v1/apps/my-app/metrics", gotPath)
	}
	if gotRange != "1h" {
		t.Errorf("range = %q, want 1h", gotRange)
	}
	out := stdout.String()
	for _, want := range []string{"App:", "my-app", "Preset:", "slow", "Observed:", "3600 requests", "p95=42.5ms", "overall: projected_success_p="} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestCmdCanarySimulate_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppMetricsResponse{
			AppID: "app-123", Range: "1h", Source: "prometheus", RequestCount: 4,
			ErrorRatePct: 2, LatencyP95MS: 10,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdCanarySimulate([]string{"my-app"}); code != 0 {
		t.Fatalf("canary simulate --json = %d, want 0", code)
	}
	var report canary.SimReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if report.Preset != "balanced" || report.ObservedTraffic != 4 {
		t.Fatalf("report = %+v, want balanced with 4 observed requests", report)
	}
	if report.Note == "" {
		t.Fatal("low-traffic report missing advisory note")
	}
}

func TestCmdCanarySimulate_RejectsDegradedMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppMetricsResponse{
			AppID: "app-123", Range: "1h", Source: "degraded: prometheus unavailable",
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	_, stderr, restore := swapIO(t)
	defer restore()

	if code := cmdCanarySimulate([]string{"my-app"}); code == 0 {
		t.Fatal("degraded metrics must fail the simulation")
	}
	if !strings.Contains(stderr(), "prometheus unavailable") {
		t.Errorf("stderr missing degraded source\nfull: %s", stderr())
	}
}

func TestCmdCanarySimulate_RejectsUnsupportedPreset(t *testing.T) {
	_, stderr, restore := swapIO(t)
	defer restore()
	if code := cmdCanarySimulate([]string{"my-app", "--canary-preset", "none"}); code == 0 {
		t.Fatal("none preset must fail for simulation")
	}
	if !strings.Contains(stderr(), "Invalid --canary-preset") {
		t.Errorf("stderr missing preset error\nfull: %s", stderr())
	}
}

func TestRun_DispatchCanary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/metrics" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.AppMetricsResponse{AppID: "app-123", Source: "prometheus", RequestCount: 5})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := run([]string{"canary", "simulate", "my-app"}); code != 0 {
		t.Errorf("run canary simulate = %d, want 0", code)
	}
}
