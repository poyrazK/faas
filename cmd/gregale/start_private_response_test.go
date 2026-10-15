package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	startResponseAppID         = "018f0000-0000-7000-8000-000000000001"
	startResponseDeploymentID  = "018f0000-0000-7000-8000-000000000002"
	startResponseInstanceID    = "018f0000-0000-7000-8000-000000000003"
	startResponseProjectID     = "018f0000-0000-7000-8000-000000000004"
	startResponseEnvironmentID = "018f0000-0000-7000-8000-000000000005"
)

func TestStartPrivateEnvironmentChecksExactDeployment(t *testing.T) {
	for _, outcome := range []string{"success", "internal workload", "wrong app", "wrong scope", "wrong deployment", "recreated environment", "old server", "application denied"} {
		t.Run(outcome, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			checks := 0
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/first-app":
					if r.URL.Query().Get("environment") != "staging" {
						t.Error("unscoped access lookup")
					}
					app := api.AppResponse{ID: startResponseAppID, RequireAuthn: true}
					if outcome == "internal workload" {
						app.RequireAuthn, app.Visibility = false, string(api.AppVisibilityInternal)
					}
					_ = json.NewEncoder(w).Encode(app)
				case "/v1/apps/first-app/deployments/" + startResponseDeploymentID + "/response-check":
					checks++
					if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+loadToken() {
						t.Error("check was not authenticated on the control plane")
					}
					var request api.DeploymentResponseCheckRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Scope != "staging" || request.AppID != startResponseAppID || request.EnvironmentID != startResponseEnvironmentID || request.ProjectID != startResponseProjectID || request.Path != "/ready?ephemeral=1" {
						t.Errorf("incorrect target: %+v %v", request, err)
					}
					response := api.DeploymentResponseCheckResult{AppID: startResponseAppID, DeploymentID: startResponseDeploymentID, InstanceID: startResponseInstanceID, Scope: "staging", ProjectID: startResponseProjectID, EnvironmentID: startResponseEnvironmentID, HTTPStatus: 200, Body: "ready"}
					switch outcome {
					case "wrong app":
						response.AppID = startResponseInstanceID
					case "wrong scope":
						response.Scope = "production"
					case "wrong deployment":
						response.DeploymentID = startResponseInstanceID
					case "recreated environment":
						response.EnvironmentID = startResponseInstanceID
					case "old server":
						http.NotFound(w, r)
						return
					case "application denied":
						response.HTTPStatus = 401
					}
					_ = json.NewEncoder(w).Encode(response)
				default:
					t.Errorf("check fell back or mutated state: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			runner := startTestRunner(t, startTestSource(t), "")
			runner.session.AppID, runner.session.DeploymentID = startResponseAppID, startResponseDeploymentID
			runner.session.ProjectID, runner.session.EnvironmentID, runner.session.Environment = startResponseProjectID, startResponseEnvironmentID, "staging"
			code := runner.sendTestRequest("/ready?ephemeral=1", "")
			if (code == 0) != (outcome == "success" || outcome == "internal workload") || checks != 1 {
				t.Fatalf("code=%d checks=%d stderr=%s stdout=%s", code, checks, stderr(), stdout)
			}
			runner.finish()
			if strings.Contains(stdout.String(), "Verified URL") || strings.Contains(stdout.String(), "Open URL:") || strings.Contains(stdout.String()+stderr(), loadToken()) {
				t.Fatal("private check exposed credentials or claimed public access")
			}
		})
	}
}
