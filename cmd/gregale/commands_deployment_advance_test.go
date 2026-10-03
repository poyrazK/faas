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

func TestDeploymentAdvanceRouteGateCLI(t *testing.T) {
	for _, scenario := range []string{"json", "report", "blocked", "missing step", "foreign result", "stale result"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			id := "11111111-1111-4111-8111-111111111111"
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request api.AdvanceCanaryRequest
				if r.Method != "POST" || r.URL.Path != "/v1/deployments/"+id+"/canary/advance" || json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedStep != 0 {
					t.Error("advance binding")
				}
				if scenario == "blocked" {
					w.WriteHeader(http.StatusConflict)
					writeJSONTest(w, api.NewProblem(http.StatusConflict, api.CodeRouteGateBlocked, "Route gate blocked", "check_stale"))
					return
				}
				result := api.CanaryAdvanceResponse{Deployment: api.DeploymentResponse{ID: id, CanaryStep: 1, TrafficPercent: 10}, AuditID: "42", RouteGate: &api.RouteGateDecision{Mode: "enforce", Status: "allowed", DeploymentID: id, Revision: 1, Reasons: []string{}}}
				switch scenario {
				case "report":
					result.RouteGate.Mode, result.RouteGate.Status, result.RouteGate.Reasons = "report", "report_only", []string{"requirements_violated"}
				case "foreign result":
					result.Deployment.ID = "22222222-2222-4222-8222-222222222222"
				case "stale result":
					result.Deployment.CanaryStep = 0
				}
				writeJSONTest(w, result)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			oldOut := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = oldOut })
			args := []string{"deployment", "advance", id, "--expected-step", "0"}
			if scenario == "json" {
				args = append(args, "--json")
			}
			if scenario == "missing step" {
				args = args[:3]
			}
			want := 1
			if scenario == "json" || scenario == "report" {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			if scenario == "report" && !strings.Contains(out.String(), "requirements_violated") {
				t.Fatal("report findings hidden")
			}
			if scenario == "json" && !strings.Contains(out.String(), `"route_gate"`) {
				t.Fatal("JSON gate evidence hidden")
			}
			if scenario == "missing step" && calls != 0 {
				t.Fatal("invalid advance reached API")
			}
		})
	}
}
