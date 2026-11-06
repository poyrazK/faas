package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPNativeReleaseChecksWebTaskNamespaceBeforePromotionAndEveryPark(t *testing.T) {
	old := *main.McpNativeCheckWorkers
	defer func() { *main.McpNativeCheckWorkers = old }()
	for _, scenario := range []struct {
		name              string
		promotions, parks int64
	}{
		{"healthy", 1, 1}, {"wrong", 0, 0}, {"missing", 0, 0}, {"sealed", 0, 0},
		{"after-promotion", 1, 0}, {"between-parks", 1, 1},
		{"before-promotion", 0, 0}, {"before-park", 1, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, _, restore := main.SwapIO(t)
			defer restore()
			t.Setenv("MCP_TASK_NAMESPACE", mcpNativeTestNamespace)
			f := newMCPVerificationFixture(t, false, 0)
			root, policy, _ := writeMCPVerificationConfig(t, 1)
			var drift atomic.Bool
			var workerChecks int
			*main.McpNativeCheckWorkers = func(context.Context, *main.Client, main.McpNativeReleasePlan, *main.McpNativeReleaseState) error {
				workerChecks++
				if scenario.name == "before-promotion" || scenario.name == "before-park" && workerChecks == 2 {
					drift.Store(true)
				}
				return nil
			}
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/web" {
					namespace := mcpNativeTestNamespace
					if drift.Load() || scenario.name == "wrong" || scenario.name == "after-promotion" && f.promotions.Load() > 0 || scenario.name == "between-parks" && f.parks.Load() > 0 {
						namespace = "different-queue"
					}
					if scenario.name == "missing" {
						namespace = ""
					}
					secrets := map[string]string{}
					if scenario.name == "sealed" {
						secrets["MCP_TASK_NAMESPACE"] = "sealed:namespace"
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app", Slug: "web", URL: f.endpoint.URL, StreamingEnabled: true, PublicAuth: api.PublicAuthStatus{Mode: api.AppPublicAuthModeOpen}, Manifest: api.AppManifest{Env: map[string]string{"MCP_TASK_NAMESPACE": namespace}, EnvSecrets: secrets}})
					return
				}
				if r.URL.Path == "/v1/apps/previous-two/deployments/latest" {
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "old-two", AppID: "previous-two", Status: main.StatusLive})
					return
				}
				f.control.Config.Handler.ServeHTTP(w, r)
			}))
			defer control.Close()
			p := main.McpNativeReleasePlan{WebApp: "web", WebPath: root, WorkerPath: root, WorkerApp: "worker", ObserverApp: "observer", ObserverMetricApp: "metrics", PreviousWorkerApps: []string{"previous"}, ReleasePolicy: policy}
			s := main.McpNativeReleaseState{Version: 1, WebDeployment: "candidate", WorkerDeployment: "worker-candidate", ServingDeployment: "stable", ServingCaptured: true, PreviousDeployments: map[string]string{"previous": "old"}, WorkerIDs: []string{"12345678-1234-4234-8234-123456789abc"}, Parked: map[string]bool{}}
			if scenario.name == "between-parks" {
				p.PreviousWorkerApps = append(p.PreviousWorkerApps, "previous-two")
				s.PreviousDeployments["previous-two"] = "old-two"
			}
			t.Setenv("MCP_TASK_RELEASE_INPUT", `{"replacementWorkerIDs":["12345678-1234-4234-8234-123456789abc"]}`)
			err := main.DrainMCPNativeRelease(t.Context(), main.NewClient(control.URL, "operator"), p, &s, filepath.Join(t.TempDir(), "state.json"))
			if (err == nil) != (scenario.name == "healthy") || f.promotions.Load() != scenario.promotions || f.parks.Load() != scenario.parks {
				t.Fatalf("namespace gate: error=%v promotions=%d parks=%d", err, f.promotions.Load(), f.parks.Load())
			}
		})
	}
}
