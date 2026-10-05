package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSynthAdapterStoredScopeSurvivesHTTPAndFencesTarget(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "scoped-synth@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "scoped-synth", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	makeTarget := func(scope string) gateway.Target {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, ImageDigest: "sha256:" + scope})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, "node", "")
		if err != nil {
			t.Fatal(err)
		}
		return gateway.Target{DeploymentID: dep.ID, InstanceID: instance.ID, NodeID: instance.NodeID, WakeID: instance.WakeID}
	}
	production, staging := makeTarget("default"), makeTarget("staging")
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID,
		DeploymentScope: "staging", Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/task", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	inv, err = store.ClaimInvocation(ctx, inv.ID, staging.InstanceID, 30)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	var wire state.Invocation
	if err := json.Unmarshal(encoded, &wire); err != nil || wire.DeploymentScope != "" {
		t.Fatalf("scope unexpectedly exposed in public envelope: %q, %v", wire.DeploymentScope, err)
	}
	calls := 0
	adapter := &synthAdapter{store: store, forward: func(target gateway.Target) http.Handler {
		if target.DeploymentID != staging.DeploymentID {
			t.Fatal("wrong scope reached forwarder")
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(http.StatusOK)
		})
	}}
	if _, _, err := adapter.InvokeWithTargetStatus(ctx, app.ID, wire, production); err == nil || calls != 0 {
		t.Fatalf("wrong environment reached worker: %v, calls=%d", err, calls)
	}
	oldWake := staging
	oldWake.WakeID = production.WakeID
	if _, _, err := adapter.InvokeWithTargetStatus(ctx, app.ID, wire, oldWake); err == nil || calls != 0 {
		t.Fatalf("stale wake reached worker: %v, calls=%d", err, calls)
	}
	out, status, err := adapter.InvokeWithTargetStatus(ctx, app.ID, wire, staging)
	if err != nil || status != http.StatusOK || calls != 1 || out.DeploymentScope != "staging" {
		t.Fatalf("scoped delivery: scope=%q status=%d calls=%d err=%v", out.DeploymentScope, status, calls, err)
	}
}
