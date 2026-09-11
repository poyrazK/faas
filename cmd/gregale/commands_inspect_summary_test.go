package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

const (
	inspectAppID = "0123456789abcdef0123456789abcdef"
	inspectDepID = "abcdef0123456789abcdef0123456789"
)

func TestCmdInspectSummary_HappyPath(t *testing.T) {
	srv := newInspectSummaryServer(t, false)
	defer srv.Close()
	configureInspectSummaryTest(t, srv.URL)

	stdout, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug}); code != 0 {
		t.Fatalf("inspect summary = %d, want 0 (stderr=%s)", code, readStderr())
	}
	body := stdout.String()
	for _, want := range []string{
		"myapp", "active · app · http", "fastapi 0.115.0", "uvicorn src.api:app",
		"/healthz · verified · 200 · 42ms", "medium · 512 MB · 1000 mCPU",
		"0→5 instances · concurrent_requests=10", "OpenAPI 3.1.0 · 2 paths · 3 endpoints",
		"2/8 upstreams · postgres, redis", inspectDepID, "balanced canary",
		"1 health gates (0 firing)", "Recommendations: none",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("summary missing %q; got:\n%s", want, body)
		}
	}
}

func TestCmdInspectSummary_JSON(t *testing.T) {
	srv := newInspectSummaryServer(t, false)
	defer srv.Close()
	configureInspectSummaryTest(t, srv.URL)
	jsonOutput = true
	t.Cleanup(resetJSONOutput)

	stdout, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug}); code != 0 {
		t.Fatalf("inspect summary json = %d, want 0 (stderr=%s)", code, readStderr())
	}
	var got inspectSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v\nraw: %s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"recommendations": []`) {
		t.Errorf("empty recommendations must encode as an array; got:\n%s", stdout.String())
	}
	if got.SchemaVersion != inspectSummarySchemaVersion {
		t.Errorf("schema_version = %d, want %d", got.SchemaVersion, inspectSummarySchemaVersion)
	}
	if got.App.WorkloadClass != "http" || got.Runtime.Framework != "fastapi" {
		t.Errorf("application intelligence drift: app=%+v runtime=%+v", got.App, got.Runtime)
	}
	if got.API.Endpoints != 3 || got.Data.Upstreams != 2 {
		t.Errorf("joined signals drift: api=%+v data=%+v", got.API, got.Data)
	}
	if got.Release.HealthGateRules != 1 || len(got.Recommendations) != 0 {
		t.Errorf("release intelligence drift: release=%+v recommendations=%+v", got.Release, got.Recommendations)
	}
	if got.Resources.Target == nil || got.Resources.Target.Metric != "concurrent_requests" {
		t.Errorf("scaling target drift: %+v", got.Resources)
	}
}

func TestInspectResources_ReportsLegacyAutoscaleTargets(t *testing.T) {
	rpsAndCPU := inspectResources(api.AppResponse{
		RAMMB: 256, CPUMillicores: 500, MaxConcurrency: 8,
		AutoscaleTargetRPS: 5, AutoscaleTargetCPUPct: 50,
	})
	if rpsAndCPU.AutoscaleTargetRPS != 5 || rpsAndCPU.AutoscaleTargetCPUPct != 50 {
		t.Fatalf("legacy targets = %+v, want rps=5/cpu=50", rpsAndCPU)
	}
	var rendered strings.Builder
	renderInspectResources(&rendered, rpsAndCPU)
	if got := rendered.String(); !strings.Contains(got, "rps=5 OR cpu_pct=50% (RPS sizing)") {
		t.Fatalf("rendered scaling target = %q, want explicit OR/RPS sizing precedence", got)
	}

	policyAndLegacy := inspectResources(api.AppResponse{
		RAMMB: 256, CPUMillicores: 500, MaxConcurrency: 8,
		AutoscaleTargetRPS: 5, AutoscaleTargetCPUPct: 50,
		ScalingPolicy: &api.ScalingPolicy{Target: &api.ScalingTarget{Metric: "concurrent_requests", Value: 10}},
	})
	rendered.Reset()
	renderInspectResources(&rendered, policyAndLegacy)
	if got := rendered.String(); !strings.Contains(got, "concurrent_requests=10 OR rps=5 OR cpu_pct=50% (RPS sizing)") {
		t.Fatalf("policy + legacy scaling target = %q, want all effective targets", got)
	}
}

func TestCmdInspectSummary_OptionalSignalsDegrade(t *testing.T) {
	srv := newInspectSummaryServer(t, true)
	defer srv.Close()
	configureInspectSummaryTest(t, srv.URL)

	stdout, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdInspect([]string{inspectSlug}); code != 0 {
		t.Fatalf("inspect degraded = %d, want 0 (stderr=%s)", code, readStderr())
	}
	body := stdout.String()
	for _, want := range []string{"api:       unavailable", "data:      unavailable", "release:   unavailable", "unavailable: alerts, deployment, openapi, upstreams"} {
		if !strings.Contains(body, want) {
			t.Errorf("degraded summary missing %q; got:\n%s", want, body)
		}
	}
	if strings.Contains(body, "No deployment was found") {
		t.Errorf("deployment lookup failure was mistaken for an absent deployment; got:\n%s", body)
	}
}

func TestInspectRecommendations_DistinguishSmokeVerifierFromHealthEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		health   *inspectHealthSummary
		code     string
		want     string
		wantNext string
	}{
		{
			name:     "verifier disabled",
			health:   &inspectHealthSummary{Status: apihostingreceipt.SmokeSkipped, ErrorCode: apihostingreceipt.SmokeErrorNotConfigured},
			want:     "health_verifier_unconfigured",
			wantNext: "FAAS_API_HOSTING_SMOKE_URL",
		},
		{
			name:     "health endpoint missing",
			health:   &inspectHealthSummary{Status: apihostingreceipt.SmokeFailed, ErrorCode: "smoke_health_path_missing"},
			want:     "health_endpoint_missing",
			wantNext: "health endpoint",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recs := inspectRecommendations(inspectSummary{
				App:     inspectAppSummary{Slug: "demo"},
				Release: inspectReleaseSummary{Available: true, Status: "live"},
				Runtime: inspectRuntimeSummary{Health: tc.health},
			})
			if len(recs) != 1 || recs[0].Code != tc.want {
				t.Fatalf("recommendations = %+v, want code %q", recs, tc.want)
			}
			if !strings.Contains(recs[0].Next, tc.wantNext) {
				t.Errorf("next = %q, want substring %q", recs[0].Next, tc.wantNext)
			}
		})
	}
}

func configureInspectSummaryTest(t *testing.T, apiURL string) {
	t.Helper()
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	resetJSONOutput()
}

func newInspectSummaryServer(t *testing.T, failOptional bool) *httptest.Server {
	t.Helper()
	app, dep := inspectSummaryFixtures(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/"+inspectSlug {
			writeInspectSummaryJSON(t, w, app)
			return
		}
		if failOptional {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			writeInspectSummaryJSON(t, w, api.Problem{Status: http.StatusForbidden, Code: "plan_feature_gated", Title: "Unavailable", Detail: "optional signal unavailable"})
			return
		}
		switch r.URL.Path {
		case "/v1/apps/" + inspectSlug + "/deployments/latest":
			writeInspectSummaryJSON(t, w, dep)
		case "/v1/apps/" + inspectSlug + "/openapi":
			if r.URL.Query().Get("source") != "auto" {
				t.Errorf("openapi source = %q, want auto", r.URL.Query().Get("source"))
			}
			_, _ = w.Write([]byte(`{"openapi":"3.1.0","paths":{"/healthz":{"get":{}},"/users":{"get":{},"post":{},"parameters":[]}}}`))
		case "/v1/apps/" + inspectSlug + "/upstreams":
			writeInspectSummaryJSON(t, w, api.DataUpstreamListResponse{Upstreams: []api.DataUpstreamResponse{
				{Kind: api.DataUpstreamKindPostgres}, {Kind: api.DataUpstreamKindRedis},
			}, Count: 2, Quota: 8})
		case "/v1/apps/" + inspectSlug + "/alerts":
			writeInspectSummaryJSON(t, w, []api.AlertRuleResponse{{AppID: inspectAppID, Enabled: true, Action: "rollback", State: "ok"}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func inspectSummaryFixtures(t *testing.T) (api.AppResponse, api.DeploymentResponse) {
	t.Helper()
	receipt, err := apihostingreceipt.Encode(apihostingreceipt.Receipt{
		SchemaVersion: apihostingreceipt.SchemaVersion,
		DeploymentID:  inspectDepID,
		AppID:         inspectAppID,
		AppURL:        "https://myapp.apps.gregale.dev",
		Profile: frameworkprofile.Profile{
			Version: frameworkprofile.Version, Framework: "fastapi", FrameworkVer: "0.115.0",
			StartCommand: "uvicorn src.api:app", Port: 8000, HealthPath: "/healthz", Inferred: true,
		},
		Smoke: apihostingreceipt.SmokeResult{
			Status: apihostingreceipt.SmokeVerified, Path: "/healthz", StatusCode: 200,
			LatencyMS: 42, VerifiedAt: time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("encode receipt: %v", err)
	}
	app := api.AppResponse{
		ID: inspectAppID, Slug: inspectSlug, URL: "https://myapp.apps.gregale.dev", Status: "active",
		Type: "app", WorkloadClass: "http", AppProtocol: "http1", RAMMB: 512, CPUMillicores: 1000,
		ResourceProfile: api.ResourceProfileMedium, MaxConcurrency: 5,
		ScalingPolicy: &api.ScalingPolicy{MinInstances: 0, MaxInstances: 5, Target: &api.ScalingTarget{Metric: "concurrent_requests", Value: 10}},
	}
	dep := api.DeploymentResponse{
		ID: inspectDepID, AppID: inspectAppID, Status: "live", Scope: "production", TrafficPercent: 100,
		CanaryPreset: "balanced", CanaryStep: 4, CanaryTotalSteps: 4, RolloutState: "complete",
		APIHostingReceipt: receipt,
	}
	return app, dep
}

func writeInspectSummaryJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
