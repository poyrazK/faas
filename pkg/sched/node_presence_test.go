package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNodePresenceTrackerRequiresTwoCompleteReports(t *testing.T) {
	tracker := newNodePresenceTracker()
	rows := []NodeTelemetry{{InstanceID: "vm-a"}}

	if _, ok := tracker.Observe("node-a", 1, rows); ok {
		t.Fatal("first report should not trigger reconciliation")
	}
	observation, ok := tracker.Observe("node-a", 1, rows)
	if !ok || !observation.shouldCheck {
		t.Fatal("second identical report should trigger reconciliation")
	}
	tracker.markReconciled("node-a", observation.fingerprint)
	if _, ok := tracker.Observe("node-a", 1, rows); ok {
		t.Fatal("steady report should not reconcile every tick")
	}

	// A count/row mismatch is inconclusive and must reset confirmation.
	if _, ok := tracker.Observe("node-a", 2, rows); ok {
		t.Fatal("incomplete report should not trigger reconciliation")
	}
	if _, ok := tracker.Observe("node-a", 1, rows); ok {
		t.Fatal("first report after reset should not trigger reconciliation")
	}

	emptyTracker := newNodePresenceTracker()
	if _, ok := emptyTracker.Observe("node-empty", 0, nil); ok {
		t.Fatal("first empty report should not trigger reconciliation")
	}
	empty, ok := emptyTracker.Observe("node-empty", 0, nil)
	if !ok {
		t.Fatal("second empty report should trigger reconciliation")
	}
	emptyTracker.markReconciled("node-empty", empty.fingerprint)
	if _, ok := emptyTracker.Observe("node-empty", 0, nil); ok {
		t.Fatal("steady empty report should not reconcile every tick")
	}
}

func TestObserveNodeInstancesFailsOnlyMissingRunningRows(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName: %v", err)
	}
	account, err := store.CreateAccount(ctx, "orphan@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "orphan-test",
		NodeID:    node.ID,
		RAMMB:     128,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:  app.ID,
		Status: state.DeployLive,
		Kind:   state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	present, err := store.CreateInstance(ctx, app.ID, deployment.ID,
		string(state.StateRunning), 128, node.ID, "wake-present")
	if err != nil {
		t.Fatalf("CreateInstance present: %v", err)
	}
	stale, err := store.CreateInstance(ctx, app.ID, deployment.ID,
		string(state.StateRunning), 128, node.ID, "wake-stale")
	if err != nil {
		t.Fatalf("CreateInstance stale: %v", err)
	}

	engine := newEngine(t, store, nil, nil, "test")
	report := []NodeTelemetry{{InstanceID: present.ID}}
	engine.ObserveNodeInstances(ctx, node.ID, 1, report)
	engine.ObserveNodeInstances(ctx, node.ID, 1, report)

	presentAfter, err := store.InstanceByID(ctx, present.ID)
	if err != nil {
		t.Fatalf("InstanceByID present: %v", err)
	}
	if presentAfter.State != string(state.StateRunning) {
		t.Fatalf("present state = %q, want running", presentAfter.State)
	}
	staleAfter, err := store.InstanceByID(ctx, stale.ID)
	if err != nil {
		t.Fatalf("InstanceByID stale: %v", err)
	}
	if staleAfter.State != string(state.StateFailed) {
		t.Fatalf("stale state = %q, want failed", staleAfter.State)
	}
	if staleAfter.TerminalAt == nil {
		t.Fatal("stale TerminalAt is nil")
	}
}
