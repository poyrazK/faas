package faas_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRouteHealthClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/demo/route-health/gate":
			_, _ = w.Write([]byte(`{"app_id":"app","mode":"report","revision":0,"routes":[]}`))
		case "PUT /v1/apps/demo/route-health/gate":
			var req faas.SetRouteHealthGateRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedRevision == nil || *req.ExpectedRevision != 0 || len(req.Routes) != 1 || req.OnRegression != "abort" || !req.Routes[0].CheckLatency || req.Routes[0].MaxP95MS != 300 {
				t.Error("intent binding")
			}
			_, _ = w.Write([]byte(`{"app_id":"app","mode":"enforce","on_regression":"abort","revision":1,"routes":[{"method":"POST","path":"/checkout","check_latency":true,"max_p95_ms":300}]}`))
		case "GET /v1/apps/demo/route-health/deployments/candidate":
			_, _ = w.Write([]byte(`{"app_id":"app","deployment_id":"candidate","status":"regressed","coverage":"observed_only","minimum_latency_requests":100,"routes":[{"method":"POST","path":"/checkout","check_latency":true,"max_p95_ms":300,"error_status":"healthy","latency_status":"regressed","windows":[{"candidate":{"requests":100,"p95_latency_ms":500},"stable":{"requests":100,"p95_latency_ms":0},"latency_delta_ms":500,"error_status":"healthy","latency_status":"regressed"}]}]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	g, err := client.GetRouteHealthGate(t.Context(), "demo")
	if err != nil || g.Mode != "report" {
		t.Fatal(err)
	}
	g, err = client.SetRouteHealthGate(t.Context(), "demo", faas.SetRouteHealthGateRequest{Mode: "enforce", OnRegression: "abort", ExpectedRevision: &g.Revision, Routes: []faas.RouteHealthRoute{{Method: "POST", Path: "/checkout", CheckLatency: true, MaxP95MS: 300}}})
	if err != nil || len(g.Routes) != 1 || g.OnRegression != "abort" || !g.Routes[0].CheckLatency || g.Routes[0].MaxP95MS != 300 {
		t.Fatal(err)
	}
	r, err := client.GetRouteHealthReport(t.Context(), "demo", "candidate")
	if err != nil || r.Status != "regressed" || r.DeploymentID != "candidate" || r.MinimumLatencyRequests != 100 || len(r.Routes) != 1 || r.Routes[0].LatencyStatus != "regressed" {
		t.Fatal(err)
	}
	w := r.Routes[0].Windows[0]
	if w.Candidate.P95LatencyMS == nil || *w.Candidate.P95LatencyMS != 500 || w.Stable.P95LatencyMS == nil || *w.Stable.P95LatencyMS != 0 || w.LatencyDeltaMS == nil || *w.LatencyDeltaMS != 500 || w.LatencyFactor != nil {
		t.Fatal("latency evidence lost zero versus unavailable distinction")
	}
}
