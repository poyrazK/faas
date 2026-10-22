package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBindingVerificationCompleteGateCLI(t *testing.T) {
	for _, scenario := range []string{"complete", "default partial", "explicit false", "missing consent", "unsupported handler", "unconfigured outbound", "write storage", "incomplete inventory", "inventory error", "inventory warning", "failed probe", "unknown binding", "no eligible", "outbound lookup failure", "human"} {
		t.Run(scenario, func(t *testing.T) {
			inventory := api.AppBindingInventory{App: "api", Complete: true, VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{
				{Type: api.BindingTypeService, Name: "billing", Binding: "billing", Scope: "app"},
				{Type: api.BindingTypeQueue, Name: "disabled", Binding: "disabled", Scope: "app", State: "disabled"},
				{Type: api.BindingTypePostgres, Name: "staging", Binding: "STAGING_URL", Scope: "staging"},
				{Type: api.BindingTypePostgres, Name: "migration", Binding: "MIGRATE_URL", Scope: "production", Access: "migration"},
			}}
			queue := api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: "app", State: "enabled", Access: "push", ConsumerState: "active", QueueCanarySupported: true}
			wantCode, blocker := 0, ""
			switch scenario {
			case "default partial", "explicit false":
				inventory.Bindings = append(inventory.Bindings, queue)
			case "missing consent", "human":
				inventory.Bindings = append(inventory.Bindings, queue)
				wantCode, blocker = 1, "queue_publish_consent_required"
			case "unsupported handler":
				queue.QueueCanarySupported = false
				inventory.Bindings = append(inventory.Bindings, queue)
				wantCode, blocker = 1, "queue_handler_unsupported"
			case "unconfigured outbound":
				inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeOutbound, Name: "provider", State: "enabled"})
				wantCode, blocker = 1, "outbound_probe_unconfigured"
			case "write storage":
				inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeObjectStorage, Name: "write", Binding: "WRITE", Scope: "production", Access: "write"})
				wantCode, blocker = 1, "write_probe_requires_opt_in"
			case "incomplete inventory":
				inventory.Complete = false
				wantCode, blocker = 1, "inventory_incomplete"
			case "inventory error":
				inventory.Issues = []api.BindingInventoryIssue{{Type: api.BindingTypePostgres, Severity: "error", Code: "query_failed", Message: "Metadata unavailable."}}
				wantCode, blocker = 1, "inventory_issue"
			case "inventory warning":
				inventory.Issues = []api.BindingInventoryIssue{{Type: api.BindingTypeQueue, Severity: "warning", Code: "observation_missing"}}
			case "failed probe":
				wantCode, blocker = 1, "probe_failed"
			case "unknown binding":
				inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: "future", Name: "future", Scope: "app"})
				wantCode, blocker = 1, "unknown_binding_type"
			case "no eligible":
				inventory.Bindings = inventory.Bindings[1:]
				wantCode, blocker = 1, "no_eligible_bindings"
			case "outbound lookup failure":
				inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeOutbound, Name: "provider", OutboundProbe: &api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}})
				wantCode, blocker = 1, "probe_not_checked"
			}
			tasks, publishes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
					main.WriteJSONTest(w, inventory)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
					main.WriteJSONTest(w, api.AppResponse{Slug: "api", ServiceBindings: []api.AppServiceBinding{{Service: "billing", Binding: "billing"}}})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/outbound-bindings":
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"title":"PRIVATE_LOOKUP_DETAIL","status":403}`))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
					tasks++
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if len(request.Command) != 2 || request.Command[0] != api.AppTaskServiceBindingProbeCommand {
						t.Errorf("unexpected task %+v", request)
					}
					task := api.AppTaskResponse{ID: "service-task", Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: main.PinnedBindingProbeOutput(api.AppTaskServiceBindingProbeCommand, "billing")}
					if scenario == "failed probe" {
						task.Status = api.AppTaskStatusFailed
						exit := 1
						task.ExitCode = &exit
					}
					main.WriteJSONTest(w, task)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/bindings/queue-canaries":
					publishes++
					t.Error("strict gate must not grant publication consent")
					http.Error(w, "not consented", 400)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			args := []string{"bindings", "verify", "api", "--all"}
			switch scenario {
			case "default partial":
			case "explicit false":
				args = append(args, "--require-complete=false")
			default:
				args = append(args, "--require-complete")
			}
			if scenario != "human" {
				args = append(args, "--json")
			}
			code, out, errs := main.CaptureBindingCLI(t, server.URL, args...)
			if code != wantCode || publishes != 0 {
				t.Fatalf("code=%d want=%d publishes=%d output=%s errors=%s", code, wantCode, publishes, out, errs)
			}
			if scenario == "human" {
				if !strings.Contains(out, "Complete coverage gate: failed") || !strings.Contains(out, blocker) {
					t.Fatalf("missing gate output: %s", out)
				}
				return
			}
			if strings.Contains(out, "PRIVATE_LOOKUP_DETAIL") {
				t.Fatalf("raw lookup error leaked: %s", out)
			}
			var report main.ServiceBindingProbeBatchReport
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatal(err)
			}
			if scenario == "default partial" || scenario == "explicit false" {
				if report.Gate != nil || report.Coverage != "partial" {
					t.Fatalf("default behavior changed: %s", out)
				}
				return
			}
			if report.Gate == nil || !report.Gate.Required || report.Gate.Passed != (wantCode == 0) {
				t.Fatalf("gate=%s", out)
			}
			if wantCode == 0 {
				if len(report.Gate.Blockers) != 0 || report.Skipped != 3 || tasks != 1 {
					t.Fatalf("unexpected exclusions: %s", out)
				}
				if !strings.Contains(out, `"blockers": []`) {
					t.Fatalf("blockers must be a JSON array: %s", out)
				}
			} else {
				found := false
				for _, item := range report.Gate.Blockers {
					if item.Code == blocker {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %s blocker: %s", blocker, out)
				}
			}
		})
	}
}

func TestCompleteGateRejectsNonBatchModesBeforeRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; t.Errorf("unexpected request %s", r.URL) }))
	defer server.Close()
	for _, args := range [][]string{
		{"bindings", "verify", "api", "billing", "--require-complete"},
		{"bindings", "verify", "api", "--release-policy", "--require-complete"},
		{"bindings", "verify", "api", "--postgres", "DATABASE_URL", "--require-complete"},
		{"bindings", "verify", "api", "--queue", "orders", "--deployment", main.PinnedBindingDeployment, "--require-complete"},
	} {
		if code, _, _ := main.CaptureBindingCLI(t, server.URL, args...); code == 0 || requests != 0 {
			t.Fatalf("args=%v code=%d requests=%d", args, code, requests)
		}
	}
}

func TestCompleteGateUnknownExclusionBlocks(t *testing.T) {
	report := main.ServiceBindingProbeBatchReport{Bindings: []main.ServiceBindingProbeBatchItem{{Type: api.BindingTypeService, Name: "billing", Status: "skipped", Reason: "future_exclusion"}}}
	gate := main.EvaluateAllBindingCoverageGate(report, true)
	if gate.Passed || len(gate.Blockers) != 1 || gate.Blockers[0].Code != "unexpected_probe_exclusion" {
		t.Fatalf("gate=%+v", gate)
	}
}
