package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDebugRequestsWatchOnceEmitsRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/debug/requests" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugTelemetryListResponse{
			Since: "1h",
			Requests: []api.DebugTelemetryRequestItem{{
				ID: "request-1", Route: "/checkout", Method: "GET", Status: 502,
				LatencyMS: 640, Count: 2, ColdBoot: true, ReceivedAt: "2026-09-09T10:00:00Z",
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

	if code := cmdDebugRequestsWatch([]string{"my-app", "--once", "--interval", "250ms"}); code != 0 {
		t.Fatalf("cmdDebugRequestsWatch() = %d, want 0", code)
	}
	got := stdout.String()
	for _, want := range []string{"Watching debugger requests", "new or changed request(s)", "request-1", "/checkout"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestCmdDebugRequestsWatchOnceJSONHeartbeat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugTelemetryListResponse{Since: "30m"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	if code := cmdDebugRequestsWatch([]string{"--once", "my-app", "--interval=250ms"}); code != 0 {
		t.Fatalf("cmdDebugRequestsWatch() = %d, want 0", code)
	}
	var event debugWatchEvent
	if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
		t.Fatalf("watch JSON = %q: %v", stdout.String(), err)
	}
	if event.Type != "heartbeat" || event.Since != "30m" {
		t.Fatalf("event = %#v, want heartbeat since=30m", event)
	}
}

func TestCmdDebugRegressionsWatchOnceJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/debug/regressions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugRegressionsResponse{
			Since: "1h",
			Regressions: []api.DebugRegressionItem{{
				DeploymentID: "dep-1", Route: "/checkout", P95MS: 900, P95BaseMS: 300,
				AffectedCount: 12, Factor: "3.00", LastDetectedAt: "2026-09-09T10:00:00Z",
			}},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	if code := cmdDebugRegressionsWatch([]string{"my-app", "--once", "--interval", "250ms"}); code != 0 {
		t.Fatalf("cmdDebugRegressionsWatch() = %d, want 0", code)
	}
	var event debugWatchEvent
	if err := json.Unmarshal(stdout.Bytes(), &event); err != nil {
		t.Fatalf("watch JSON = %q: %v", stdout.String(), err)
	}
	if event.Type != "regression" || event.Regression == nil || event.Regression.Route != "/checkout" {
		t.Fatalf("event = %#v, want regression for /checkout", event)
	}
}

func TestCmdDebugBundleWritesRedactedInvestigation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/apps/my-app/debug/requests/request-1/evidence":
			_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{
				Request:     api.DebugTelemetryRequestItem{ID: "request-1", Route: "/checkout", Status: 502},
				Explanation: api.DebugEvidenceExplanation{Headline: "slow request"},
			})
		case r.URL.Path == "/v1/apps/my-app/debug/regressions":
			_ = json.NewEncoder(w).Encode(api.DebugRegressionsResponse{
				Since: "1h", Regressions: []api.DebugRegressionItem{{DeploymentID: "dep-1", Route: "/checkout", Factor: "2.00"}},
			})
		case r.URL.Path == "/v1/apps/my-app/debug/compare":
			_ = json.NewEncoder(w).Encode(api.DebugCompareResponse{
				Source: "source", Mirror: "mirror", Routes: []api.DebugCompareRouteStats{{Route: "/checkout", SourceP95: 300, MirrorP95: 350}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	outPath := filepath.Join(t.TempDir(), "incident.json")
	if code := cmdDebugBundle([]string{
		"my-app", "request-1", "--since", "1h", "--source", "source", "--mirror", "mirror", "--output", outPath,
	}); code != 0 {
		t.Fatalf("cmdDebugBundle() = %d, want 0", code)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle debugIncidentBundle
	if err := json.Unmarshal(b, &bundle); err != nil {
		t.Fatalf("bundle JSON: %v", err)
	}
	if bundle.SchemaVersion != debugIncidentBundleSchema || bundle.Compare == nil || bundle.Request.ID != "request-1" {
		t.Fatalf("bundle = %#v", bundle)
	}
	if bundle.Redaction.Profile != "debugger-safe" || len(bundle.Redaction.Excluded) == 0 {
		t.Fatalf("redaction = %#v", bundle.Redaction)
	}
	if strings.Contains(string(b), "request_body") == false {
		t.Fatalf("bundle should document excluded request_body field: %s", b)
	}
	if mode := fileMode(t, outPath); mode.Perm() != 0o600 {
		t.Fatalf("bundle mode = %o, want 600", mode.Perm())
	}
	if !strings.Contains(stdout.String(), "Wrote redacted debugger bundle") {
		t.Fatalf("human output = %q", stdout.String())
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}
