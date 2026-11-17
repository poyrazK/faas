package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// A delivered invocation is recorded like a service call, with its source,
// status and serving instance; one that got no response is not (prod
// findings 2026-11-15 #7: cron calls were missing from metrics and logs).
func TestSynthAdapterRecordsDeliveredInvocations(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "synth-telemetry@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "synth-telemetry", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:telemetry"})
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
	target := gateway.Target{DeploymentID: dep.ID, InstanceID: instance.ID, NodeID: instance.NodeID, WakeID: instance.WakeID}

	var got []gateway.ServiceRequestObservation
	var paths []string
	observer := &invocationRequestObserver{}
	observer.set(func(r *http.Request, o gateway.ServiceRequestObservation) {
		got = append(got, o)
		paths = append(paths, r.Method+" "+r.URL.Path)
	})
	status := http.StatusNotFound
	adapter := &synthAdapter{store: store, requestObserver: observer, forward: func(gateway.Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	}}
	deliver := func() {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/report", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		inv, err = store.ClaimInvocation(ctx, inv.ID, instance.ID, 30)
		if err != nil {
			t.Fatal(err)
		}
		if _, code, err := adapter.InvokeWithTargetStatus(ctx, app.ID, inv, target); err != nil || code != status {
			t.Fatalf("delivery: status %d, %v; want %d", code, err, status)
		}
	}
	deliver()
	if len(got) != 1 {
		t.Fatalf("observations = %d, want 1", len(got))
	}
	o := got[0]
	if o.InvocationSource != string(state.InvocationAsyncInvoke) || o.Status != http.StatusNotFound || o.AccountID != account.ID ||
		o.Target.InstanceID != instance.ID || o.ReceivedAt.IsZero() || paths[0] != "POST /report" {
		t.Fatalf("observation = %+v at %s", o, paths[0])
	}

	// Without an observer wired, delivery still works and records nothing.
	adapter.requestObserver = nil
	deliver()
	if len(got) != 1 {
		t.Fatalf("observations without an observer = %d, want 1", len(got))
	}
}
