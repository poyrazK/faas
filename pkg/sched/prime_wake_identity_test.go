package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestPrimeStampsNodeIDOnBootEnvelope: vmmd's wake.readiness_200 takes
// node_id from the wire envelope, and pkg/events rejects a joinable wake row
// without it. Prime (the deploy pipeline's first boot) reaches the runtime
// with no inbound envelope; after rc.282 each prime raised
// FaasWakeEventIdentityMissing because the rebuilt envelope carried app,
// instance and wake IDs but no node.
func TestPrimeStampsNodeIDOnBootEnvelope(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	if err := e.Prime(ctx, app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListInstancesForApp(ctx, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("prime rows: %v %v", rows, err)
	}
	fields, ok := wire.FromContext(vmm.LastColdBootCtx())
	if !ok {
		t.Fatal("prime's vmmd-bound context has no correlation fields")
	}
	if fields.NodeID == "" || fields.NodeID != rows[0].NodeID {
		t.Fatalf("envelope NodeID = %q, want the prime instance's node %q", fields.NodeID, rows[0].NodeID)
	}
	if fields.WakeID != rows[0].WakeID || fields.AppID != app.ID || fields.InstanceID != rows[0].ID {
		t.Fatalf("envelope = %+v, want wake/app/instance of %+v", fields, rows[0])
	}
}
