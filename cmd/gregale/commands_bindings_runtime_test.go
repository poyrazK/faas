package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBindingRuntimeRenderingKeepsPassedVerificationSeparate(t *testing.T) {
	previous := osStdout
	var output bytes.Buffer
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	renderAppBindingInventory(api.AppBindingInventory{App: "api", Complete: true,
		Bindings: []api.AppBindingInventoryItem{{Type: "postgres", Name: "primary", Binding: "DATABASE_URL", Scope: "production", VerificationStatus: "passed",
			Refresh: &api.BindingRefresh{WakeID: "wake-1", Status: "retrying", Attempts: 2, FailureReason: "telemetry_missing"}}},
		RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: time.Now().UTC(), Deployments: []api.BindingRuntimeDeployment{
			{DeploymentID: "deploy-1", Scope: "production", DeploymentStatus: "live", Status: "stale", Serving: api.BindingRuntimeInstanceCounts{Stale: 2}, Resident: api.BindingRuntimeInstanceCounts{Stale: 3}},
		}},
	})
	for _, want := range []string{"CONFIG FRESHNESS", "passed", "stale", "2 serving instances", "resident current=0 stale=3", "retrying (telemetry_missing)", "wake_id=wake-1", "does not confirm credential use"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %s", want, output.String())
		}
	}
}

func TestBindingRuntimeSummaryUsesResourceScopeAndAppWideBindings(t *testing.T) {
	runtime := &api.BindingRuntimeFreshness{Deployments: []api.BindingRuntimeDeployment{
		{Scope: "production", Status: "stale"}, {Scope: "staging", Status: "current"}, {Scope: "app", Status: "updating"},
	}}
	for _, tc := range []struct{ kind, scope, want string }{
		{"postgres", "production", "stale"}, {"object_storage", "staging", "current"},
		{"postgres", "app", "updating"}, {"postgres", "missing", "inactive"},
		{"service", "app", "stale"}, {"queue", "app", "stale"}, {"outbound", "app", "stale"},
	} {
		if got := humanBindingRuntimeFreshness(runtime, api.AppBindingInventoryItem{Type: tc.kind, Scope: tc.scope}); got != tc.want {
			t.Fatalf("%s %s: %s want %s", tc.kind, tc.scope, got, tc.want)
		}
	}
	if got := humanBindingRuntimeFreshness(nil, api.AppBindingInventoryItem{}); got != "unknown" {
		t.Fatalf("missing runtime=%s", got)
	}
}

func TestCmdBindingsJSONIncludesRuntimeAndRefreshInOneRequest(t *testing.T) {
	stamp := time.Now().UTC()
	want := api.AppBindingInventory{App: "api", Complete: true, Bindings: []api.AppBindingInventoryItem{
		{Type: "postgres", Scope: "staging", Refresh: &api.BindingRefresh{WakeID: "wake-1", Status: "failed", FailureReason: "restart_attempt_failed"}},
	}, RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: stamp, ConfigChangedAt: &stamp,
		Deployments: []api.BindingRuntimeDeployment{{DeploymentID: "deploy-1", Scope: "staging", Status: "stale", Serving: api.BindingRuntimeInstanceCounts{Stale: 1}, Resident: api.BindingRuntimeInstanceCounts{Stale: 1}}}}}
	code, got, _ := runBindingInventoryTest(t, want, "staging", "--json", "bindings", "api", "--scope", "staging")
	if code != 0 || got.RuntimeFreshness == nil || got.RuntimeFreshness.Deployments[0].Serving.Stale != 1 || got.Bindings[0].Refresh.Status != "failed" {
		t.Fatalf("exit=%d inventory=%+v", code, got)
	}
}
