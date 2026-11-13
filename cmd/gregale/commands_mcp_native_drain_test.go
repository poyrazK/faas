package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativeDrainReceipt(instance string, budget any) string {
	event := map[string]any{"event": "mcp_task_worker_started", "workerID": "12345678-1234-1234-1234-123456789abc", "claimFence": true}
	if budget != nil {
		event["shutdownTimeoutMs"] = budget
	}
	line, _ := json.Marshal(event)
	envelope, _ := json.Marshal(map[string]string{"instance": instance, "line": string(line)})
	return "event: log\ndata: " + string(envelope) + "\n\n"
}

func TestMCPNativeDrainBudgetUsesCurrentReplicaReceipts(t *testing.T) {
	instances := []api.InstanceResponse{{ID: "one", DeploymentID: "current", State: "running"}, {ID: "two", DeploymentID: "current", State: "running"}, {ID: "old", DeploymentID: "previous", State: "running"}}
	for _, tc := range []struct {
		name, logs string
		want       time.Duration
	}{
		{"maximum actual budget", nativeDrainReceipt("one", 10000) + nativeDrainReceipt("two", 120000) + nativeDrainReceipt("old", 300000), 120 * time.Second},
		{"missing budget", nativeDrainReceipt("one", nil) + nativeDrainReceipt("two", 10000), 0},
		{"missing replica", nativeDrainReceipt("one", 10000), 0},
		{"invalid budget", nativeDrainReceipt("one", -1) + nativeDrainReceipt("two", 10000), 0},
		{"fractional milliseconds", nativeDrainReceipt("one", 1000.5) + nativeDrainReceipt("two", 10000), 0},
		{"too large", nativeDrainReceipt("one", 300001) + nativeDrainReceipt("two", 10000), 0},
		{"ambiguous receipt", nativeDrainReceipt("one", 10000) + nativeDrainReceipt("one", 120000) + nativeDrainReceipt("two", 10000), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget, err := main.McpNativeDrainBudget(strings.NewReader(tc.logs), instances, "current")
			if (err == nil) != (tc.want != 0) || budget != tc.want {
				t.Fatalf("budget=%s err=%v", budget, err)
			}
		})
	}
}

func TestMCPNativeDrainBudgetGuardsPromotionAndEveryPark(t *testing.T) {
	oldCheck := *main.McpNativeCheckWorkers
	*main.McpNativeCheckWorkers = func(context.Context, *main.Client, main.McpNativeReleasePlan, *main.McpNativeReleaseState) error {
		return nil
	}
	defer func() { *main.McpNativeCheckWorkers = oldCheck }()
	for _, tc := range []struct {
		name              string
		grace             time.Duration
		budget            any
		drift             bool
		zeroFirst         bool
		promotions, parks int64
	}{
		{"default ten seconds", 20 * time.Second, 10000, false, false, 1, 1},
		{"override longer than grace", 45 * time.Second, 120000, false, false, 0, 0},
		{"budget equals grace", 20 * time.Second, 20000, false, false, 0, 0},
		{"grace changes before park", 45 * time.Second, 10000, true, false, 1, 0},
		{"legacy receipt", 45 * time.Second, nil, false, false, 0, 0},
		{"zero pool starts before park", 45 * time.Second, 120000, false, true, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_TASK_NAMESPACE", mcpNativeTestNamespace)
			f := newMCPVerificationFixture(t, false, 0)
			root, policy, _ := writeMCPVerificationConfig(t, 1)
			reads := 0
			instanceReads := 0
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/previous/instances":
					instanceReads++
					if tc.zeroFirst && instanceReads == 1 {
						_ = json.NewEncoder(w).Encode([]api.InstanceResponse{})
					} else {
						f.control.Config.Handler.ServeHTTP(w, r)
					}
				case "/v1/apps/previous":
					reads++
					grace := tc.grace
					if tc.drift && reads > 1 {
						grace = 10 * time.Second
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "previous", WorkloadClass: "worker", Manifest: api.AppManifest{StopGracePeriod: grace}})
				case "/v1/apps/previous/logs":
					fmt.Fprint(w, nativeDrainReceipt("old-instance", tc.budget))
				default:
					f.control.Config.Handler.ServeHTTP(w, r)
				}
			}))
			defer control.Close()
			plan := main.McpNativeReleasePlan{WebApp: "web", WebPath: root, WorkerPath: root, WorkerApp: "worker", ObserverApp: "observer", ObserverMetricApp: "metrics", PreviousWorkerApps: []string{"previous"}, ReleasePolicy: policy}
			state := main.McpNativeReleaseState{Version: 1, WebDeployment: "candidate", WorkerDeployment: "worker-candidate", ServingDeployment: "stable", ServingCaptured: true, PreviousDeployments: map[string]string{"previous": "old"}, WorkerIDs: []string{"12345678-1234-4234-8234-123456789abc"}, Parked: map[string]bool{}}
			t.Setenv("MCP_TASK_RELEASE_INPUT", `{"replacementWorkerIDs":["12345678-1234-4234-8234-123456789abc"]}`)
			err := main.DrainMCPNativeRelease(t.Context(), main.NewClient(control.URL, "operator"), plan, &state, filepath.Join(t.TempDir(), "state.json"))
			if (err == nil) != (tc.parks == 1) || f.promotions.Load() != tc.promotions || f.parks.Load() != tc.parks {
				t.Fatalf("drain error=%v promotions=%d parks=%d", err, f.promotions.Load(), f.parks.Load())
			}
		})
	}
}
