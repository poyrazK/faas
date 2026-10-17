package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

func TestBindingsQueueRequirementsCLIConfirmation(t *testing.T) {
	for _, scenario := range []string{"set", "clear", "ignored requirement", "old server", "duplicate", "invalid mode", "conflicting clear"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/v1/apps/demo/bindings/release-policy-with-queues" {
					t.Errorf("unsafe route %s %s", r.Method, r.URL)
				}
				if scenario == "old server" {
					http.NotFound(w, r)
					return
				}
				var request api.SetBindingReleasePolicyRequest
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.QueueRequirements == nil {
					t.Error("explicit queue requirements lost")
				}
				now := time.Now().UTC()
				policy := api.BindingReleasePolicy{AppID: releaseCheckAppID, Scope: "default", Mode: "enforce", Revision: 1, MaxVerificationAge: "10m0s", UpdatedAt: &now, QueueRequirements: request.QueueRequirements}
				if scenario == "ignored requirement" {
					policy.QueueRequirements = nil
				}
				writeJSONTest(w, policy)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			args := []string{"bindings", "release-policy", "set", "demo", "--mode", "enforce", "--expected-revision", "0", "--json"}
			if scenario == "clear" {
				args = append(args, "--clear-queue-requirements")
			} else {
				args = append(args, "--queue-require", "orders=consumer_readiness")
			}
			switch scenario {
			case "duplicate":
				args = append(args, "--queue-require", "orders=consumer_readiness")
			case "invalid mode":
				args = append(args, "--queue-require", "other=connectivity")
			case "conflicting clear":
				args = append(args, "--clear-queue-requirements")
			}
			if code := run(args); (code == 0) != (scenario == "set" || scenario == "clear") {
				t.Fatalf("wrong confirmation: %d %s", code, out.String())
			}
			wantCalls := 1
			if scenario == "duplicate" || scenario == "invalid mode" || scenario == "conflicting clear" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("invalid flags or fallback reached API: %d calls, want %d", calls, wantCalls)
			}
		})
	}
}

func TestBindingsQueueRequirementsCLIReadOnlyCheck(t *testing.T) {
	inventory := bindingCheckCLIInventory()
	polled, environment := time.Now().UTC(), "production"
	inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: environment, QueueEnvironment: &environment, State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &polled})
	code, out, _ := runBindingCheckCLI(t, inventory, "", "bindings", "check", "api", "--queue-require", "orders=consumer_readiness", "--json")
	report := decodeBindingCheckCLI(t, out)
	if code != 0 || !report.Passed || report.Coverage != "partial" || report.AllowUnsupported || report.QueueRequirements["orders"] != api.QueueEvidenceConsumerReadiness {
		t.Fatalf("readiness check: %d %s", code, out)
	}
}

func TestBindingsQueueRequirementsPolicyCheckConfirmsMap(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "changed"}[changed], func(t *testing.T) {
			policy := storageReleaseCheckPolicy()
			policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceConsumerReadiness}
			polled, environment := time.Now().UTC(), "production"
			reads := 0
			code, report, errs := runStorageReleaseCheckCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/api":
					writeJSONTest(w, api.AppResponse{ID: releaseCheckAppID, Slug: "api"})
				case "/v1/apps/api/bindings/release-policy":
					reads++
					if changed && reads > 1 {
						policy.QueueRequirements = map[string]string{}
					}
					writeJSONTest(w, policy)
				case "/v1/apps/api/bindings":
					inventory := storageReleaseCheckInventory(r.URL.Query().Get("scope"), r.URL.Query().Get("deployment_id"), "read_write")
					inventory.Bindings = append(inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: environment, QueueEnvironment: &environment, State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &polled})
					writeJSONTest(w, inventory)
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.NotFound(w, r)
				}
			}, "--release-policy")
			if (code == 0) == changed || report.Passed == changed || report.ReleasePolicyConfirmed == changed || report.QueueRequirements["orders"] != api.QueueEvidenceConsumerReadiness {
				t.Fatalf("queue policy confirmation: %d %+v %s", code, report, errs)
			}
			if changed && !strings.Contains(errs, "policy changed") {
				t.Fatalf("map mutation missing diagnostic: %s", errs)
			}
		})
	}
}

func TestBindingsQueueRequirementsWaitObservesScheduledPoll(t *testing.T) {
	for _, state := range []string{"stale", "unobserved", "paused"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			client := bindingCheckWaitClient{read: func(_ context.Context, _ string, scope, deployment string) (api.AppBindingInventory, error) {
				calls++
				inventory := bindingCheckCLIInventory()
				inventory.Scope, inventory.RequestedDeploymentID = scope, deployment
				environment, polled := "production", time.Now().UTC()
				queue := api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: environment, QueueEnvironment: &environment, State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &polled}
				if calls == 1 {
					switch state {
					case "stale":
						polled = polled.Add(-time.Minute)
					case "unobserved":
						queue.ObservedAt, queue.ConsumerLiveness = nil, "not_observed"
					case "paused":
						queue.ConsumerState = "paused"
					}
				}
				inventory.Bindings = append(inventory.Bindings, queue)
				return inventory, nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			report, err := pollBindingCheck(ctx, client, bindingcheck.Policy{App: "api", MaxVerificationAge: time.Minute * 10, QueueRequirements: map[string]string{"orders": api.QueueEvidenceConsumerReadiness}}, true, time.Millisecond)
			if err != nil || report.Passed != (state != "paused") || calls != map[bool]int{true: 2, false: 1}[state != "paused"] {
				t.Fatalf("queue wait mutated or failed: calls=%d report=%+v err=%v", calls, report, err)
			}
		})
	}
}

func TestBindingsVerifyAllSkipsReleaseOnlyCredentials(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	created := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/apps/api/bindings":
			writeJSONTest(w, api.AppBindingInventory{App: "api", Complete: true, VerificationScope: "default", Bindings: []api.AppBindingInventoryItem{
				{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "default", Access: "read_write"},
				{Type: api.BindingTypePostgres, Name: "primary", Binding: "MIGRATION_DATABASE_URL", Scope: "default", Access: "migration"},
			}})
		case r.Method == "POST" && r.URL.Path == "/v1/apps/api/tasks":
			created++
			var request api.CreateAppTaskRequest
			if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Command) != 2 || request.Command[1] != "DATABASE_URL" {
				t.Errorf("migration probe admitted: %+v", request)
			}
			body := `{"environment_key":"DATABASE_URL","environment":{"status":"passed"},"configuration":{"status":"passed"},"connection":{"status":"passed"},"query":{"status":"passed"}}`
			writeJSONTest(w, api.AppTaskResponse{ID: "runtime-probe", Status: api.AppTaskStatusSucceeded, StdoutTail: body, ExitCode: new(int)})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	if code := run([]string{"bindings", "verify", "api", "--all", "--json"}); code != 0 {
		t.Fatalf("verify all: %d %s", code, out.String())
	}
	var report serviceBindingProbeBatchReport
	if json.Unmarshal(out.Bytes(), &report) != nil || created != 1 || report.Checked != 1 || report.Skipped != 1 || report.Bindings[1].Reason != "release_only" {
		t.Fatalf("release credential was probed: %s", out.String())
	}
}
