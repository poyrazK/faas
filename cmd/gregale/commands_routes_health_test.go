package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

const healthCandidateID = "22222222-2222-4222-8222-222222222222"

func cliHealthReport(status string) api.RouteHealthReport {
	now := time.Now().UTC()
	r := api.RouteHealthReport{AppID: "11111111-1111-4111-8111-111111111111", DeploymentID: healthCandidateID, StableDeploymentID: "33333333-3333-4333-8333-333333333333", Mode: "enforce", Revision: 1, CheckedAt: now, Routes: []api.RouteHealthFinding{{Method: "POST", Path: "/checkout", Windows: routehealth.Windows(now)}}}
	for i := range r.Routes[0].Windows {
		w := &r.Routes[0].Windows[i]
		w.Candidate.Requests = 100
		w.Stable.Requests = 100
		if status == "regressed" {
			w.Candidate.ServerErrors = 10
		}
		if status == "unknown" {
			w.Candidate.Requests = 1
		}
	}
	anchor := now.Add(-time.Hour)
	routehealth.Evaluate(&r, &anchor, "")
	return r
}
func TestRouteHealthCLIReportsAndFailures(t *testing.T) {
	for _, scenario := range []string{"healthy", "regressed", "unknown", "wrong_deployment", "false_healthy", "invalid_counts", "human"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			status := scenario
			if status != "regressed" && status != "unknown" {
				status = "healthy"
			}
			report := cliHealthReport(status)
			if scenario == "wrong_deployment" {
				report.DeploymentID = report.StableDeploymentID
			}
			if scenario == "false_healthy" {
				report.Routes[0].Windows[0].Candidate.Requests = 1
			}
			if scenario == "invalid_counts" {
				report.Routes[0].Windows[0].Stable.ServerErrors = 101
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/route-health/deployments/"+healthCandidateID {
					t.Error("route binding")
				}
				writeJSONTest(w, report)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "health", "report", "demo", "--deployment", healthCandidateID, "--fail-on-unhealthy"}
			if scenario != "human" {
				args = append(args, "--json")
			}
			want := 1
			if scenario == "healthy" || scenario == "human" {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			if scenario == "human" && (!strings.Contains(out.String(), "POST /checkout") || !strings.Contains(out.String(), "0/100") || !strings.Contains(out.String(), "full capture unknown")) {
				t.Fatal("missing customer evidence")
			}
		})
	}
}
func TestRouteHealthCLIConfiguration(t *testing.T) {
	for _, scenario := range []string{"get", "set", "abort", "invalid_action", "wrong_action", "missing_revision", "unknown_field", "wrong_routes"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			file := filepath.Join(t.TempDir(), "routes.json")
			body := `[{"method":"POST","path":"/checkout"}]`
			if scenario == "unknown_field" {
				body = `[{"method":"POST","path":"/checkout","typo":true}]`
			}
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			g := api.RouteHealthGate{AppID: "11111111-1111-4111-8111-111111111111", Mode: "enforce", Revision: 1, UpdatedAt: &now, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout"}}}
			if scenario == "abort" {
				g.OnRegression = "abort"
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/apps/demo/route-health/gate" {
					t.Error("configuration path")
				}
				if scenario != "get" {
					var req api.SetRouteHealthGateRequest
					if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedRevision == nil || *req.ExpectedRevision != 0 || len(req.Routes) != 1 || req.OnRegression != routehealth.RegressionAction(g.OnRegression) {
						t.Error("configuration binding")
					}
				}
				if scenario == "wrong_action" {
					g.OnRegression = "abort"
				}
				if scenario == "wrong_routes" {
					g.Routes[0].Path = "/other"
				}
				writeJSONTest(w, g)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			args := []string{"routes", "health", "set", "demo", "--routes", file, "--mode", "enforce", "--json"}
			if scenario == "abort" {
				args = append(args, "--on-regression", "abort")
			}
			if scenario == "invalid_action" {
				args = append(args, "--on-regression", "delete")
			}
			if scenario != "missing_revision" {
				args = append(args, "--expected-revision", "0")
			}
			if scenario == "get" {
				args = []string{"routes", "health", "get", "demo", "--json"}
			}
			want := 1
			if scenario == "get" || scenario == "set" || scenario == "abort" {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d", code, want)
			}
			if (scenario == "unknown_field" || scenario == "missing_revision" || scenario == "invalid_action") && calls != 0 {
				t.Fatal("invalid intent reached API")
			}
		})
	}
}
