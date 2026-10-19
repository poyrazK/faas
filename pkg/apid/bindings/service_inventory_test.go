package bindings

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManagedServiceInventoryFrozenNamespace(t *testing.T) {
	c, caller, target := explanationFixture(t, true)
	// The physical app settings deliberately disagree with the frozen graph.
	caller.Manifest.ServiceBindings = api.ServiceBindingsForTargets([]string{"unrelated"})
	items, issues := ManagedServiceInventory(t.Context(), c, caller, c.deployment, "production")
	if len(issues) != 0 || len(items) != 1 {
		t.Fatalf("items=%+v issues=%+v", items, issues)
	}
	item := items[0]
	ctx := item.ServiceContext
	if item.Name != "billing" || item.Binding != "BILLING_URL" || item.Scope != "production" || ctx == nil || ctx.TargetApp != "git-billing" || ctx.TargetAppID != target || ctx.CallerDeploymentID != c.deployment.ID || ctx.ReleaseID != c.route.ReleaseSetID || ctx.RoutingStatus != "resolved" || item.ServiceTargetDeploymentID != c.route.TargetDeploymentID || item.Transport != "https" {
		t.Fatalf("frozen service context=%+v item=%+v", ctx, item)
	}
	if item.VerificationStatus != "unsupported" || ctx.VerificationSupported || ctx.VerificationReason != api.ManagedServiceVerificationUnsupported || item.Verification != nil {
		t.Fatal("metadata created verification evidence")
	}
	if c.globalReads != 0 {
		t.Fatal("resolved frozen binding through global app name")
	}
	again, _ := ManagedServiceInventory(t.Context(), c, caller, c.deployment, "production")
	if len(ctx.Revision) != 64 || again[0].ServiceContext.Revision != ctx.Revision {
		t.Fatal("revision changes on an unchanged read")
	}
	for _, change := range []func(){
		func() { c.route.TargetDeploymentID = uuid.NewString() },
		func() { c.route.ReleaseSetID = uuid.NewString() },
		func() { c.frozen.Generation++; c.freeze(t) },
	} {
		before := again[0].ServiceContext.Revision
		change()
		again, issues = ManagedServiceInventory(t.Context(), c, caller, c.deployment, "production")
		if len(issues) != 0 || again[0].ServiceContext.Revision == before {
			t.Fatalf("context change retained revision: %+v %v", again, issues)
		}
	}
}

func TestManagedServiceInventoryIncompleteContexts(t *testing.T) {
	for _, tc := range []struct {
		name, status, reason string
		incomplete           bool
		change               func(*explanationCatalog, string)
	}{
		{"unbound", "denied", "service_not_bound", false, func(c *explanationCatalog, _ string) { c.found = false }},
		{"inactive release", "denied", "managed_route_denied", false, func(c *explanationCatalog, _ string) { c.routeErr = state.ErrConflict }},
		{"database unavailable", "unavailable", "managed_route_unavailable", true, func(c *explanationCatalog, _ string) { c.routeErr = errors.New("PRIVATE_ERROR") }},
		{"foreign target", "unavailable", "service_target_unavailable", true, func(c *explanationCatalog, id string) {
			a := c.apps[id]
			a.AccountID = uuid.NewString()
			c.apps[id] = a
		}},
		{"wrong graph target", "unavailable", "managed_context_changed", true, func(c *explanationCatalog, _ string) { c.route.TargetAppID = uuid.NewString() }},
		{"held candidate", "unavailable", "qualification_context_unsupported", false, func(c *explanationCatalog, _ string) { held := true; c.deployment.EnvironmentWorkloadHeldValue = &held }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, caller, target := explanationFixture(t, true)
			tc.change(c, target)
			items, issues := ManagedServiceInventory(t.Context(), c, caller, c.deployment, "")
			if len(items) != 1 || (len(issues) > 0) != tc.incomplete {
				t.Fatalf("items=%+v issues=%+v", items, issues)
			}
			ctx := items[0].ServiceContext
			if ctx.RoutingStatus != tc.status || ctx.RoutingReason != tc.reason || items[0].VerificationStatus != "unsupported" || c.globalReads != 0 {
				t.Fatalf("context=%+v", ctx)
			}
			if tc.name == "foreign target" && (ctx.TargetAppID != "" || ctx.TargetApp != "") {
				t.Fatal("foreign identity leaked")
			}
			raw, _ := json.Marshal(struct {
				Items  []api.AppBindingInventoryItem
				Issues []api.BindingInventoryIssue
			}{items, issues})
			if strings.Contains(string(raw), "PRIVATE_ERROR") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestManagedServiceInventoryNeverFallsBack(t *testing.T) {
	c, caller, _ := explanationFixture(t, true)
	caller.Manifest.ServiceBindings = api.ServiceBindingsForTargets([]string{"legacy"})
	c.frozen.ServiceBindings = nil
	c.freeze(t)
	items, issues := ManagedServiceInventory(t.Context(), c, caller, c.deployment, "")
	if len(items) != 0 || len(issues) != 0 || c.globalReads != 0 {
		t.Fatalf("empty frozen namespace fell back: %+v %v", items, issues)
	}
	c.deployment.EnvironmentWorkloadRuntime = "{malformed"
	items, issues = ManagedServiceInventory(t.Context(), c, caller, c.deployment, "")
	if len(items) != 0 || len(issues) != 1 || issues[0].Code != "service_context_invalid" {
		t.Fatalf("invalid frozen namespace fell back: %+v %v", items, issues)
	}
	c, caller, _ = explanationFixture(t, true)
	items, issues = ManagedServiceInventory(t.Context(), c, caller, c.deployment, "staging")
	if len(issues) != 1 || issues[0].Code != "service_deployment_scope_mismatch" || items[0].ServiceTargetDeploymentID != "" {
		t.Fatalf("scope mismatch resolved a route: %+v %v", items, issues)
	}
}
