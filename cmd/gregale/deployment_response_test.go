package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeploymentResponseCheckPinsEvidenceAndReportsApplicationStatus(t *testing.T) {
	for _, outcome := range []string{"success", "internal", "application denied", "wrong deployment", "wrong scope", "wrong app", "unsupported", "named environment"} {
		t.Run(outcome, func(t *testing.T) {
			stdout, stderr := main.StartTestEnvironment(t)
			appID, deploymentID, instanceID := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
			projectID, environmentID := strings.Repeat("d", 32), strings.Repeat("e", 32)
			scope := "default"
			if outcome == "named environment" {
				scope = "staging"
			}
			checks := 0
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/deployments/" + deploymentID:
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: deploymentID, AppID: appID, Scope: scope, Status: "live"})
				case "/v1/apps/first-app":
					app := api.AppResponse{ID: appID, Slug: "first-app", RequireAuthn: true}
					if outcome == "wrong app" {
						app.ID = instanceID
					}
					if outcome == "internal" {
						app.Visibility = "internal"
					}
					_ = json.NewEncoder(w).Encode(app)
				case "/v1/apps/first-app/deployments/" + deploymentID + "/response-check":
					checks++
					if r.Method != http.MethodPost || r.Header.Get("Authorization") == "" {
						t.Error("check did not use authenticated control plane")
					}
					if outcome == "unsupported" {
						http.NotFound(w, r)
						return
					}
					var request api.DeploymentResponseCheckRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.AppID != appID || request.Scope != scope || request.Path != "/ready" {
						t.Fatalf("request=%+v err=%v", request, err)
					}
					result := api.DeploymentResponseCheckResult{AppID: appID, DeploymentID: deploymentID, InstanceID: instanceID, Scope: scope, HTTPStatus: 200, Body: "ready"}
					switch outcome {
					case "application denied":
						result.HTTPStatus = 401
					case "wrong deployment":
						result.DeploymentID = instanceID
					case "wrong scope":
						result.Scope = "other"
					case "named environment":
						if request.ProjectID != projectID || request.EnvironmentID != environmentID {
							t.Fatal("environment identities missing")
						}
						result.ProjectID, result.EnvironmentID = projectID, environmentID
					}
					_ = json.NewEncoder(w).Encode(result)
				default:
					t.Errorf("unexpected fallback or mutation: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			args := []string{"--json", "release", "verify", deploymentID, "--app", "first-app", "--path", "/ready"}
			if scope != "default" {
				args = append(args, "--project-id", projectID, "--environment-id", environmentID)
			}
			code := main.Run(args)
			success := outcome == "success" || outcome == "internal" || outcome == "named environment"
			if (code == 0) != success {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr())
			}
			if outcome == "wrong app" && checks != 0 {
				t.Fatal("checked an unrelated app")
			}
			if success || outcome == "application denied" {
				var receipt struct {
					Verified             bool `json:"verified"`
					PublicAccessVerified bool `json:"public_access_verified"`
					HTTPStatus           int  `json:"http_status"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil || receipt.Verified != success || receipt.PublicAccessVerified || receipt.HTTPStatus == 0 {
					t.Fatalf("receipt=%+v err=%v output=%s", receipt, err, stdout)
				}
			}
		})
	}
}

func TestDeploymentResponseCheckRejectsUnsafePathsWithoutNetwork(t *testing.T) {
	for _, path := range []string{"https://example.test", "//example.test", "/bad#fragment", "/bad\\path"} {
		t.Run(path, func(t *testing.T) {
			main.StartTestEnvironment(t)
			calls := 0
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) { calls++; http.NotFound(w, r) })
			if code := main.Run([]string{"release", "verify", strings.Repeat("b", 32), "--app", "first-app", "--path", path}); code == 0 || calls != 0 {
				t.Fatalf("code=%d calls=%d", code, calls)
			}
		})
	}
}
