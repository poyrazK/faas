package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdBindingsVerifyExactServiceTarget(t *testing.T) {
	const target = "5b87c415-7c93-4932-acab-a3c90e98be86"
	for _, echo := range []string{target, "", pinnedBindingDeployment} {
		t.Run("echo="+echo, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/api/bindings":
					_ = json.NewEncoder(w).Encode(api.AppBindingInventory{App: "api", Complete: true, RequestedDeploymentID: pinnedBindingDeployment, VerificationDeploymentID: pinnedBindingDeployment, VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING_URL", Scope: "app"}}})
				case "/v1/apps/api/tasks":
					posts++
					var req api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.VerificationDeploymentID != pinnedBindingDeployment || len(req.Command) != 3 || req.Command[2] != target {
						t.Errorf("exact selection lost: %+v %v", req, err)
					}
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task", DeploymentID: pinnedBindingDeployment, DeploymentScope: "production", Status: api.AppTaskStatusQueued})
				case "/v1/apps/api/tasks/task":
					passed := api.ServiceBindingProbeCheck{Status: "passed"}
					output, _ := json.Marshal(api.ServiceBindingProbeReport{Service: "billing", TargetDeploymentID: echo, DNS: passed, TLS: passed, Authorization: passed, Routing: passed})
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task", DeploymentID: pinnedBindingDeployment, DeploymentScope: "production", Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: string(output)})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			code, out, err := captureBindingCLI(t, server.URL, "bindings", "verify", "api", "billing", "--deployment", pinnedBindingDeployment, "--target-deployment", target, "--poll-interval", "1ms", "--json")
			if (code == 0) != (echo == target) || posts != 1 {
				t.Fatalf("exit=%d posts=%d output=%s %s", code, posts, out, err)
			}
		})
	}
}
