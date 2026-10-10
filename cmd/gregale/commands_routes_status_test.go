package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routestatus"
)

func routeStatusTestServer(t *testing.T, telemetryErr bool) *httptest.Server {
	t.Helper()
	budget := int64(100)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("routes status sent a write: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/apps/demo/deployments":
			writeJSONTest(w, api.DeploymentListResponse{Items: []api.DeploymentResponse{
				{ID: "11111111-1111-4111-8111-111111111111", Status: "live", TrafficPercent: 90},
				{ID: "22222222-2222-4222-8222-222222222222", Status: "live", TrafficPercent: 10, CanaryStep: 1, CanaryTotalSteps: 4},
				{ID: "33333333-3333-4333-8333-333333333333", Status: "superseded"},
			}})
		case "/v1/apps/demo/analytics/route-customers":
			if r.URL.Query().Get("deployment_id") != "11111111-1111-4111-8111-111111111111" {
				t.Errorf("usage read the wrong deployment: %s", r.URL.RawQuery)
			}
			if telemetryErr {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: 403, Code: "plan_feature_gated", Title: "Plan", Detail: "analytics is not included in this plan"})
				return
			}
			writeJSONTest(w, api.RouteCustomerUsageResponse{Routes: []api.RouteCustomerUsage{
				{Route: "POST /checkout", Method: "POST", Requests: 900, PlatformTenantCount: 40},
				{Route: "GET /debug", Method: "GET", Requests: 50, PlatformTenantCount: 1},
			}})
		case "/v1/apps/demo/deployments/11111111-1111-4111-8111-111111111111/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{Doc: map[string]any{"paths": map[string]any{"/checkout": map[string]any{"post": map[string]any{"operationId": "checkout"}}}}})
		case "/v1/apps/demo/route-health/gate":
			writeJSONTest(w, api.RouteHealthGate{Mode: "report", Revision: 1, Routes: []api.RouteHealthRoute{}})
		case "/v1/apps/demo/route-monitor":
			writeJSONTest(w, api.RouteMonitorConfig{Enabled: true, OnViolation: "rollback", Revision: 2, Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget}}})
		case "/v1/apps/demo/route-monitor/report":
			writeJSONTest(w, api.RouteMonitorReport{Routes: []api.RouteMonitorFinding{{Route: api.RouteMonitorRoute{Method: "POST", Path: "/checkout"}, Status: "healthy"}}})
		case "/v1/apps/demo/route-monitor/incidents":
			writeJSONTest(w, api.RouteMonitorIncidentPage{Incidents: []api.RouteMonitorIncident{}})
		case "/v1/apps/demo/route-requirements":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: 404, Code: "not_found", Title: "Not found"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func runRouteStatusCLI(t *testing.T, server *httptest.Server, args ...string) (int, string) {
	t.Helper()
	resetJSONOut(t)
	setPreviewTestAuth(t)
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	code := run(append([]string{"routes", "status", "demo"}, args...))
	return code, output.String()
}

func TestRoutesStatusCLIJoinsReadOnlySources(t *testing.T) {
	server := routeStatusTestServer(t, false)
	defer server.Close()
	code, out := runRouteStatusCLI(t, server, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	var s routestatus.Status
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatal(err)
	}
	if s.ServingDeploymentID != "11111111-1111-4111-8111-111111111111" || s.CandidateDeployment != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("deployments = %s / %s", s.ServingDeploymentID, s.CandidateDeployment)
	}
	if len(s.Routes) != 2 || s.Routes[0].Path != "/checkout" || s.Routes[0].Protection != "rollback" || s.Routes[1].Path != "/debug" {
		t.Fatalf("routes = %+v", s.Routes)
	}
	if s.Summary.Unprotected != 1 || len(s.Unavailable) != 0 {
		t.Fatalf("summary = %+v unavailable = %v (a missing requirements file is not an error)", s.Summary, s.Unavailable)
	}
}

func TestRoutesStatusCLIReportsUnavailableSections(t *testing.T) {
	server := routeStatusTestServer(t, true)
	defer server.Close()
	code, out := runRouteStatusCLI(t, server)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"Routes for demo", "POST", "/checkout", "rollback healthy", "Unavailable:", "traffic: analytics is not included in this plan"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
