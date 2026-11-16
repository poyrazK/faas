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
	for _, outcome := range []string{"success", "internal", "application denied", "wrong deployment", "wrong scope", "wrong app", "unsupported", "named environment", "named environment lookup", "dashed id", "redirect", "denied"} {
		t.Run(outcome, func(t *testing.T) {
			stdout, stderr := main.StartTestEnvironment(t)
			appID, deploymentID, instanceID := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
			projectID, environmentID := strings.Repeat("d", 32), strings.Repeat("e", 32)
			scope := "default"
			if strings.HasPrefix(outcome, "named environment") {
				scope = "staging"
			}
			checks := 0
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/deployments/" + deploymentID, "/v1/deployments/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb":
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: deploymentID, AppID: appID, Scope: scope, Status: "live"})
				case "/v1/apps/first-app":
					app := api.AppResponse{ID: appID, Slug: "first-app", RequireAuthn: true, ProjectID: projectID}
					if outcome == "wrong app" {
						app.ID = instanceID
					}
					if outcome == "internal" {
						app.Visibility = "internal"
					}
					_ = json.NewEncoder(w).Encode(app)
				case "/v1/projects":
					if outcome != "named environment lookup" {
						t.Error("looked up the project although identities were given")
					}
					_ = json.NewEncoder(w).Encode([]api.ProjectSummaryResponse{{ID: strings.Repeat("f", 32), Slug: "other"}, {ID: projectID, Slug: "shop"}})
				case "/v1/projects/shop/environments/staging":
					_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: environmentID, ProjectID: projectID, Slug: "staging"})
				case "/v1/apps/first-app/deployments/" + deploymentID + "/response-check":
					checks++
					if r.Method != http.MethodPost || r.Header.Get("Authorization") == "" {
						t.Error("check did not use authenticated control plane")
					}
					if outcome == "unsupported" {
						http.NotFound(w, r)
						return
					}
					if outcome == "denied" {
						w.Header().Set("Content-Type", "application/problem+json")
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte(`{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden"}`))
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
					case "redirect":
						result.HTTPStatus = 302
					case "named environment", "named environment lookup":
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
			requested := deploymentID
			if outcome == "dashed id" {
				requested = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
			}
			args := []string{"--json", "release", "verify", requested, "--app", "first-app", "--path", "/ready"}
			if outcome == "named environment" {
				args = append(args, "--project-id", projectID, "--environment-id", environmentID)
			}
			code := main.Run(args)
			success := outcome == "success" || outcome == "internal" || strings.HasPrefix(outcome, "named environment") || outcome == "dashed id"
			if (code == 0) != success {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr())
			}
			if outcome == "wrong app" && checks != 0 {
				t.Fatal("checked an unrelated app")
			}
			if (outcome == "unsupported" || outcome == "denied") && !strings.Contains(stderr(), map[string]string{"unsupported": "incompatible", "denied": "deployment-write access"}[outcome]) {
				t.Fatalf("stderr=%s", stderr())
			}
			if success || outcome == "application denied" || outcome == "redirect" {
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
