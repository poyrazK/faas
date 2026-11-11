package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPNativeReleaseAcceptsScaledToZeroPreviousPool(t *testing.T) {
	t.Setenv("MCP_TASK_NAMESPACE", "review11")
	t.Setenv("MCP_TASK_RELEASE_INPUT", `{"previousWorkerIDs":[]}`)
	root := t.TempDir()
	body, err := os.ReadFile("templates/mcp-node/gregale-mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	config["tasks"].(map[string]any)["enabled"] = true
	body, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gregale-mcp.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	mutations := 0
	previousInstancesRead := false
	deployReached := false
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
			http.Error(w, "unexpected mutation", 500)
			return
		}
		switch r.URL.Path {
		case "/v1/deployments/candidate-resume":
			deployReached = true
			http.Error(w, "controlled stop after previous-worker preflight", http.StatusServiceUnavailable)
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(api.CapabilitiesResponse{ConditionalParking: true})
		case "/v1/account":
			json.NewEncoder(w).Encode(api.AccountResponse{Plan: "pro"})
		case "/v1/apps/web":
			json.NewEncoder(w).Encode(api.AppResponse{ID: "web", Manifest: api.AppManifest{Env: map[string]string{"MCP_TASK_NAMESPACE": "review11", "MCP_TASKS_ROLE": "web"}}})
		case "/v1/apps/observer":
			json.NewEncoder(w).Encode(api.AppResponse{ID: "observer", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 1}, Env: map[string]string{"MCP_TASK_NAMESPACE": "review11", "MCP_TASKS_ROLE": "observer", "MCP_TASKS_SCALING_APP_SLUG": "metrics"}}})
		case "/v1/apps/observer/instances":
			json.NewEncoder(w).Encode([]api.InstanceResponse{{ID: "observer-instance", State: "running"}})
		case "/v1/apps/metrics/custom-metrics":
			json.NewEncoder(w).Encode(api.CustomMetricListResponse{Metrics: []api.CustomMetricResponse{{Name: "mcp_tasks_observer_heartbeat", Value: 1, ObservedAt: time.Now()}}})
		case "/v1/apps/candidate":
			json.NewEncoder(w).Encode(api.AppResponse{ID: "candidate", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 1}}})
		case "/v1/apps/candidate/instances":
			json.NewEncoder(w).Encode([]api.InstanceResponse{})
		case "/v1/apps/candidate/deployments":
			json.NewEncoder(w).Encode(api.DeploymentListResponse{})
		case "/v1/apps/web/deployments":
			json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: "stable", Status: main.StatusLive, TrafficPercent: 100}}})
		case "/v1/apps/previous":
			json.NewEncoder(w).Encode(api.AppResponse{ID: "previous", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 0}, StopGracePeriod: 45 * time.Second}})
		case "/v1/apps/previous/deployments/latest":
			json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "old", AppID: "previous", Status: main.StatusLive})
		case "/v1/apps/previous/instances":
			previousInstancesRead = true
			json.NewEncoder(w).Encode([]api.InstanceResponse{})
		case "/v1/apps/previous/logs":
			t.Error("empty previous pool must not require logs")
			w.WriteHeader(500)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer control.Close()
	plan := main.McpNativeReleasePlan{WebApp: "web", WorkerApp: "candidate", ObserverApp: "observer", ObserverMetricApp: "metrics", WebPath: root, WorkerPath: root, PreviousWorkerApps: []string{"previous"}}
	state := main.McpNativeReleaseState{Version: 1, WorkerDeployment: "candidate-resume", PreviousDeployments: map[string]string{}, Parked: map[string]bool{}}
	journal := filepath.Join(t.TempDir(), "state.json")
	err = main.StartMCPNativeRelease(context.Background(), main.NewClient(control.URL, "test-operator"), plan, &state, journal)
	if err == nil || strings.Contains(err.Error(), "startup") || !deployReached {
		t.Fatalf("zero-replica preflight failed before the controlled deployment read: %v", err)
	}
	if !previousInstancesRead || mutations != 0 || !state.ServingCaptured || state.PreviousDeployments["previous"] != "old" {
		t.Fatalf("previous read=%v mutations=%d captured=%v generation=%q", previousInstancesRead, mutations, state.ServingCaptured, state.PreviousDeployments["previous"])
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("preflight journal missing: %v", err)
	}
}
