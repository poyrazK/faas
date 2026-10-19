package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedServiceProbeCLIRequiresCurrentReceipt(t *testing.T) {
	for _, mode := range []string{"single", "all", "worker-single", "worker-all", "worker-complete-all"} {
		for _, scenario := range []string{"exact", "old-guest", "release-changed", "unsupported"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				slug := "api"
				if strings.HasPrefix(mode, "worker-") {
					slug = "worker"
				}
				resetJSONOut(t)
				_, restoreStderr := captureStderr(t)
				defer restoreStderr()
				item := managedCLIInventory()
				item.VerificationStatus = "unknown"
				item.ServiceContext.VerificationSupported = true
				item.ServiceContext.VerificationContract = api.ManagedServiceProbeContract
				item.ServiceContext.VerificationReason = ""
				spec, valid := api.ManagedServiceProbeForItem(item)
				if !valid {
					t.Fatal("invalid managed inventory fixture")
				}
				if scenario == "unsupported" {
					item.ServiceContext.VerificationSupported = false
				}
				taskID, appID := uuid.NewString(), uuid.NewString()
				var admitted, published atomic.Int32
				now := time.Now().UTC().Add(-time.Second)
				receipt := api.QueueBindingCanary{ID: uuid.NewString(), Binding: "orders", DeploymentID: pinnedBindingDeployment, Scope: "production", ReleaseID: spec.ReleaseID, Status: "succeeded", PublishedAt: now, ExpiresAt: now.Add(5 * time.Minute), DeliveredAt: &now, CompletedAt: &now}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.Method + " " + r.URL.Path {
					case "GET /v1/apps/" + slug + "/bindings":
						if r.URL.Query().Get("deployment_id") != pinnedBindingDeployment {
							t.Errorf("missing deployment selector: %s", r.URL)
						}
						current, c := item, *item.ServiceContext
						current.ServiceContext = &c
						if admitted.Load() != 0 {
							current.VerificationStatus = "passed"
							current.Verification = &api.BindingVerification{TaskID: taskID, Result: "passed", Source: "task_guest", DeploymentID: pinnedBindingDeployment, Scope: "production", ManagedService: &spec}
							if scenario == "release-changed" {
								c.ReleaseID = uuid.NewString()
							}
						}
						bindings := []api.AppBindingInventoryItem{current}
						if slug == "worker" {
							q := api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: "production", State: "enabled", Access: "push", ConsumerState: "active", QueueCanarySupported: mode == "worker-complete-all"}
							if published.Load() != 0 {
								q.QueueCanary = &receipt
							}
							bindings = append(bindings, q)
						}
						_ = json.NewEncoder(w).Encode(api.AppBindingInventory{App: slug, Complete: true, ServiceBindingSource: "environment_release", RequestedDeploymentID: pinnedBindingDeployment, VerificationDeploymentID: pinnedBindingDeployment, VerificationScope: "production", Bindings: bindings})
					case "POST /v1/apps/" + slug + "/bindings/queue-canaries":
						if mode != "worker-complete-all" {
							t.Error("unexpected queue publication")
						}
						var req api.CreateQueueBindingCanaryRequest
						if json.NewDecoder(r.Body).Decode(&req) != nil || !req.PublishConsent || req.DeploymentID != pinnedBindingDeployment || req.Binding != "orders" {
							t.Errorf("unconfirmed queue admission: %+v", req)
						}
						published.Add(1)
						_ = json.NewEncoder(w).Encode(receipt)
					case "POST /v1/apps/" + slug + "/tasks":
						admitted.Add(1)
						var request api.CreateAppTaskRequest
						if json.NewDecoder(r.Body).Decode(&request) != nil || request.VerificationDeploymentID != pinnedBindingDeployment || len(request.Command) != 3 || request.Command[0] != api.AppTaskServiceBindingTargetProbeCommand || request.Command[2] != spec.TargetDeploymentID {
							t.Errorf("unconfirmed managed request: %+v", request)
						}
						check := api.ServiceBindingProbeCheck{Status: "passed"}
						report := api.ServiceBindingProbeReport{ManagedService: &spec, Service: spec.Service, URL: "https://billing.internal", TargetDeploymentID: spec.TargetDeploymentID, HTTPStatus: 204, DNS: check, TLS: check, Authorization: check, Routing: check}
						if scenario == "old-guest" {
							report.ManagedService = nil
						}
						raw, _ := json.Marshal(report)
						zero := 0
						_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: taskID, AppID: appID, DeploymentID: pinnedBindingDeployment, DeploymentScope: "production", Kind: api.AppTaskKindManual, Status: api.AppTaskStatusSucceeded, Command: request.Command, StdoutTail: string(raw), ExitCode: &zero})
					default:
						t.Errorf("unexpected request during isolated service verification: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "fp_test_x")
				var output bytes.Buffer
				old := osStdout
				osStdout = &output
				t.Cleanup(func() { osStdout = old })
				args := []string{"bindings", "verify", slug, "billing", "--deployment", pinnedBindingDeployment, "--json"}
				if strings.HasSuffix(mode, "all") {
					args = []string{"bindings", "verify", slug, "--all", "--require-complete", "--deployment", pinnedBindingDeployment, "--json"}
				}
				if mode == "worker-complete-all" {
					args = append(args, "--queue-publish")
				}
				code := run(args)
				if (code == 0) != (scenario == "exact" && mode != "worker-all") {
					t.Fatalf("code=%d output=%s", code, &output)
				}
				if mode == "worker-complete-all" && scenario == "exact" {
					var report serviceBindingProbeBatchReport
					if json.Unmarshal(output.Bytes(), &report) != nil || report.Coverage != "complete" || report.Passed != 2 || report.Checked != 2 || report.Gate == nil || !report.Gate.Passed || published.Load() != 1 {
						t.Fatalf("mixed worker coverage did not complete: %s", &output)
					}
				}
				if scenario == "unsupported" && admitted.Load() != 0 {
					t.Fatal("unsupported context admitted work")
				}
				if scenario != "unsupported" && admitted.Load() != 1 {
					t.Fatalf("probe admissions=%d output=%s", admitted.Load(), &output)
				}
				if scenario == "release-changed" && !strings.Contains(output.String(), "changed") {
					t.Fatalf("missing stale context reason: %s", &output)
				}
				if mode == "worker-all" && scenario == "exact" {
					var report serviceBindingProbeBatchReport
					if json.Unmarshal(output.Bytes(), &report) != nil || report.Coverage != "partial" || report.Total != 2 || report.Checked != 1 || report.Passed != 1 || report.Failed != 0 || report.Unsupported != 1 || report.Gate == nil || report.Gate.Passed || len(report.Gate.Blockers) != 1 || report.Gate.Blockers[0].Code != "queue_handler_unsupported" {
						t.Fatalf("worker batch lost HTTPS evidence or waived queue coverage: %s", &output)
					}
				}
			})
		}
	}
}
