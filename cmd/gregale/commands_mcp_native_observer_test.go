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

const mcpNativeTestNamespace = "reports-production"

func mcpNativeObserverFixtureEnv() map[string]string {
	return map[string]string{"MCP_TASKS_ROLE": "observer", "MCP_TASK_NAMESPACE": mcpNativeTestNamespace, "MCP_TASKS_SCALING_APP_SLUG": "metrics"}
}

func TestMCPNativeReleaseChecksObserverBindingsBeforePromotionAndRetirement(t *testing.T) {
	old := *main.McpNativeCheckWorkers
	defer func() { *main.McpNativeCheckWorkers = old }()
	*main.McpNativeCheckWorkers = func(context.Context, *main.Client, main.McpNativeReleasePlan, *main.McpNativeReleaseState) error {
		return nil
	}
	for _, test := range []struct {
		name, namespace, target                       string
		missingRelease, sealedNamespace, sealedTarget bool
		drift                                         bool
	}{
		{name: "healthy", namespace: mcpNativeTestNamespace, target: "metrics"},
		{name: "wrong namespace", namespace: "other-queue", target: "metrics"},
		{name: "missing namespace", target: "metrics"},
		{name: "missing release namespace", namespace: mcpNativeTestNamespace, target: "metrics", missingRelease: true},
		{name: "sealed namespace", namespace: mcpNativeTestNamespace, target: "metrics", sealedNamespace: true},
		{name: "wrong metric destination", namespace: mcpNativeTestNamespace, target: "another-app"},
		{name: "sealed metric destination", namespace: mcpNativeTestNamespace, target: "metrics", sealedTarget: true},
		{name: "namespace changes before retirement", namespace: mcpNativeTestNamespace, target: "metrics", drift: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, restore := main.SwapIO(t)
			defer restore()
			namespace := mcpNativeTestNamespace
			if test.missingRelease {
				namespace = ""
			}
			t.Setenv("MCP_TASK_NAMESPACE", namespace)
			f := newMCPVerificationFixture(t, false, 0)
			root, policy, _ := writeMCPVerificationConfig(t, 1)
			var observerReads atomic.Int64
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/observer" {
					f.control.Config.Handler.ServeHTTP(w, r)
					return
				}
				env := mcpNativeObserverFixtureEnv()
				env["MCP_TASK_NAMESPACE"], env["MCP_TASKS_SCALING_APP_SLUG"] = test.namespace, test.target
				if observerReads.Add(1) > 1 && test.drift {
					env["MCP_TASK_NAMESPACE"] = "other-queue"
				}
				secrets := map[string]string{}
				if test.sealedNamespace {
					secrets["MCP_TASK_NAMESPACE"] = "secret:namespace"
				}
				if test.sealedTarget {
					secrets["MCP_TASKS_SCALING_APP_SLUG"] = "secret:metric-target"
				}
				_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "observer", Slug: "observer", WorkloadClass: "worker", Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Min: 1}, Env: env, EnvSecrets: secrets}})
			}))
			defer control.Close()
			p := main.McpNativeReleasePlan{WebApp: "web", WebPath: root, WorkerPath: root, WorkerApp: "worker", ObserverApp: "observer", ObserverMetricApp: "metrics", PreviousWorkerApps: []string{"previous"}, ReleasePolicy: policy}
			s := main.McpNativeReleaseState{Version: 1, WebDeployment: "candidate", WorkerDeployment: "worker-candidate", ServingDeployment: "stable", ServingCaptured: true, PreviousDeployments: map[string]string{"previous": "old"}, WorkerIDs: []string{"12345678-1234-4234-8234-123456789abc"}, Parked: map[string]bool{}}
			t.Setenv("MCP_TASK_RELEASE_INPUT", `{"replacementWorkerIDs":["12345678-1234-4234-8234-123456789abc"]}`)
			err := main.DrainMCPNativeRelease(t.Context(), main.NewClient(control.URL, "operator"), p, &s, filepath.Join(t.TempDir(), "state.json"))
			wantPromotions, wantParks := int64(0), int64(0)
			if test.name == "healthy" {
				wantPromotions, wantParks = 1, 1
			} else if test.drift {
				wantPromotions = 1
			}
			if (err == nil) != (test.name == "healthy") || f.promotions.Load() != wantPromotions || f.parks.Load() != wantParks {
				t.Fatalf("incorrect observer gated release: error=%v promotions=%d parks=%d", err, f.promotions.Load(), f.parks.Load())
			}
		})
	}
}
