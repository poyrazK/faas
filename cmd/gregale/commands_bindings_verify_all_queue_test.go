package main

import (
	"bytes"
	"context"
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

func TestAllBindingQueueVerification(t *testing.T) {
	for _, scenario := range []string{"completed", "no consent", "unsupported handler", "release unavailable", "pull", "paused", "disabled", "outside scope", "ambiguous", "failed", "wrong deployment", "wrong release", "wrong scope", "wrong binding", "incomplete receipt", "timeout", "unpinned consent"} {
		t.Run(scenario, func(t *testing.T) {
			deployment := pinnedBindingDeployment
			now := time.Now().UTC().Add(-time.Second)
			receipt := api.QueueBindingCanary{ID: uuid.NewString(), Binding: "orders", DeploymentID: deployment, Scope: "production", Status: "published", PublishedAt: now, ExpiresAt: now.Add(time.Minute)}
			queue := api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: "app", State: "enabled", Access: "push", ConsumerState: "active", QueueCanarySupported: true}
			switch scenario {
			case "unsupported handler":
				queue.QueueCanarySupported = false
			case "release unavailable":
				queue.QueueCanarySupported = false
				queue.QueueCanaryReason = "queue_canary_context_unavailable"
			case "pull":
				queue.Access = "pull"
			case "paused":
				queue.ConsumerState = "paused"
			case "disabled":
				queue.State = "disabled"
			case "outside scope":
				queue.Scope = "staging"
			}
			inventory := api.AppBindingInventory{App: "api", Complete: true, RequestedDeploymentID: deployment, VerificationDeploymentID: deployment, VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{queue, {Type: api.BindingTypeService, Name: "billing", Binding: "billing", Scope: "app"}}}
			if scenario == "ambiguous" {
				inventory.Bindings = append(inventory.Bindings, queue)
			}
			publishes, polls, cancels, tasks, reads := 0, 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
					reads++
					if r.URL.Query().Get("deployment_id") != deployment {
						t.Errorf("inventory not pinned: %s", r.URL)
					}
					writeJSONTest(w, inventory)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
					tasks++
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.VerificationDeploymentID != deployment {
						t.Errorf("task not pinned: %+v", request)
					}
					writeJSONTest(w, api.AppTaskResponse{ID: "service-task", DeploymentID: deployment, DeploymentScope: "production", Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: `{"service":"billing","dns":{"status":"passed"},"tls":{"status":"passed"},"authorization":{"status":"passed"},"routing":{"status":"passed"}}`})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/bindings/queue-canaries":
					publishes++
					var request api.CreateQueueBindingCanaryRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if !request.PublishConsent || request.DeploymentID != deployment || request.Binding != "orders" {
						t.Errorf("request=%+v", request)
					}
					switch scenario {
					case "wrong deployment":
						receipt.DeploymentID = uuid.NewString()
					case "wrong scope":
						receipt.Scope = "staging"
					case "wrong binding":
						receipt.Binding = "other"
					}
					writeJSONTest(w, receipt)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings/queue-canaries/"+receipt.ID:
					if scenario == "wrong release" {
						receipt.ReleaseID = uuid.NewString()
					}
					polls++
					if scenario != "timeout" {
						receipt.Status = "succeeded"
						receipt.DeliveredAt = &now
						receipt.CompletedAt = &now
						if scenario == "failed" {
							receipt.Status = "failed"
							receipt.Reason = "handler_failed"
						}
						if scenario == "incomplete receipt" {
							receipt.CompletedAt = nil
						}
					}
					writeJSONTest(w, receipt)
				case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/api/bindings/queue-canaries/"+receipt.ID:
					cancels++
					receipt.Status = "cancelled"
					writeJSONTest(w, receipt)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			args := []string{"bindings", "verify", "api", "--all", "--deployment", deployment, "--poll-interval", "1ms", "--wait-timeout", "1s", "--json"}
			if scenario == "completed" || scenario == "timeout" {
				args = append(args, "--require-complete")
			}
			if scenario != "no consent" {
				args = append(args, "--queue-publish")
			}
			if scenario == "timeout" {
				for i, arg := range args {
					if arg == "1s" {
						args[i] = "50ms"
					}
				}
			}
			if scenario == "unpinned consent" {
				args = []string{"bindings", "verify", "api", "--all", "--queue-publish", "--json"}
			}
			code, out, errs := captureBindingCLI(t, server.URL, args...)
			if scenario == "unpinned consent" {
				if code == 0 || reads+publishes+tasks != 0 {
					t.Fatalf("invalid selection caused requests code=%d", code)
				}
				return
			}
			var report serviceBindingProbeBatchReport
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("%v output=%s errors=%s", err, out, errs)
			}
			shouldPublish := scenario == "completed" || scenario == "failed" || scenario == "wrong deployment" || scenario == "wrong release" || scenario == "wrong scope" || scenario == "wrong binding" || scenario == "incomplete receipt" || scenario == "timeout"
			if shouldPublish && publishes != 1 || !shouldPublish && publishes != 0 {
				t.Fatalf("publishes=%d report=%s", publishes, out)
			}
			switch scenario {
			case "completed":
				if code != 0 || report.Passed != 2 || report.Coverage != "complete" || polls != 1 || cancels != 0 || tasks != 1 || report.Bindings[0].QueueReport == nil || report.Gate == nil || !report.Gate.Passed {
					t.Fatalf("code=%d polls=%d cancels=%d tasks=%d report=%s", code, polls, cancels, tasks, out)
				}
			case "failed", "wrong deployment", "wrong release", "wrong scope", "wrong binding", "incomplete receipt", "timeout":
				wantCancels := 1
				if scenario == "wrong deployment" || scenario == "wrong scope" || scenario == "wrong binding" {
					wantCancels = 0
				}
				if code == 0 || report.Failed != 1 || cancels != wantCancels {
					t.Fatalf("code=%d cancels=%d report=%s errors=%s", code, cancels, out, errs)
				}
			case "release unavailable":
				if publishes != 0 || !strings.Contains(out, "queue_canary_context_unavailable") {
					t.Fatalf("misleading release eligibility: %s", out)
				}
			case "disabled", "outside scope":
				if code != 0 || report.Skipped != 1 || report.Passed != 1 {
					t.Fatalf("code=%d report=%s", code, out)
				}
			default:
				if code != 0 || report.Unsupported == 0 || report.Coverage != "partial" || tasks != 1 {
					t.Fatalf("code=%d report=%s", code, out)
				}
				reason := map[string]string{"no consent": "queue_publish_consent_required", "unsupported handler": "queue_handler_unsupported", "pull": "queue_delivery_unsupported", "paused": "queue_consumer_not_ready", "ambiguous": "queue_binding_ambiguous"}[scenario]
				if !strings.Contains(out, reason) {
					t.Fatalf("missing reason %s: %s", reason, out)
				}
			}
		})
	}
}

func TestAllBindingQueueCancellation(t *testing.T) {
	resetJSONOut(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now().UTC().Add(-time.Second)
	receipt := api.QueueBindingCanary{ID: uuid.NewString(), Binding: "orders", DeploymentID: pinnedBindingDeployment, Scope: "production", Status: "published", PublishedAt: now, ExpiresAt: now.Add(time.Minute)}
	var cancels atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/v1/apps/api/bindings/queue-canaries" {
				t.Errorf("unexpected publication %s", r.URL.Path)
			}
		case http.MethodGet:
			cancel()
		case http.MethodDelete:
			cancels.Add(1)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(receipt)
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "fixture")
	inventory := api.AppBindingInventory{App: "api", Complete: true, RequestedDeploymentID: pinnedBindingDeployment, VerificationDeploymentID: pinnedBindingDeployment, VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: "app", Access: "push", ConsumerState: "active", QueueCanarySupported: true}}}
	selected := &deploymentBindingProbeClient{Client: client, slug: "api", inventory: inventory}
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	jsonOutput = true
	t.Cleanup(func() { osStdout = old })
	code := runAllBindingProbes(ctx, selected, "api", time.Millisecond, time.Second, allBindingProbeOptions{client: client, publish: true, requireComplete: true})
	if code != 130 || cancels.Load() != 1 {
		t.Fatalf("code=%d cancellations=%d report=%s", code, cancels.Load(), output.String())
	}
	var report serviceBindingProbeBatchReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Passed != 0 || report.NotChecked != 1 || report.Bindings[0].Reason != "interrupted" || report.Gate == nil || report.Gate.Passed {
		t.Fatalf("report=%+v", report)
	}
}
