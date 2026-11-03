// adr: 1151, 20261102072403 — a thaw books its VM back on the node under the
// durable reservation, as an admission does.
package sched_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A frozen row is outside its node's bookings, and a thaw is a same-node
// update the node guard let through, so a thaw put its plan RAM back past the
// node's cap unchecked (invariant 2). Another app's VM now fills the node:
// the thaw's publication is refused, the resumed VM parks with reason
// no_capacity, and the row is never published RUNNING.
func TestThawRefusedOnANodeBookedFull(t *testing.T) {
	for _, fast := range []bool{false, true} {
		name := "ordinary thaw"
		if fast {
			name = "ticketed thaw"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := sched.NewFrozenFixture(t, true)
			ops := wire.NewOpsMetrics("schedd")
			(*f.E()).WithOpsMetrics(ops)
			(*f.E()).EnableFrozenTier()
			if fast {
				(*f.E()).EnableFrozenFastPath()
			}
			if handled, err := (*f.E()).FreezeIdle(ctx, (*f.Ins()).ID, 150); err != nil || !handled {
				t.Fatalf("FreezeIdle: %v, %v", handled, err)
			}
			store := *f.Store()
			node, err := store.ComputeNodeByID(ctx, (*f.Ins()).NodeID)
			if err != nil || node.AdmissionCeilingMB <= 0 {
				t.Fatalf("node %+v, %v: the test needs a node ceiling", node, err)
			}
			fillApp, err := store.CreateApp(ctx, state.App{AccountID: (*f.App()).AccountID, Slug: "filler",
				Type: state.AppTypeFunction, RAMMB: 512, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			fillDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: fillApp.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:filler", Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			// The filler fits on its own; the frozen VM's ram_mb + 8 does not.
			fill := node.AdmissionCeilingMB - api.PerVMOverheadMB - (*f.App()).RAMMB/2
			if _, err := store.CreateInstanceWithMode(ctx, fillApp.ID, fillDep.ID, string(state.StateRunning), fill, node.ID,
				uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
				t.Fatalf("filler instance: %v", err)
			}

			_, _ = (*f.E()).Wake(ctx, (*f.App()).ID, "", "", "")

			row, err := store.InstanceByID(ctx, (*f.Ins()).ID)
			if err != nil || row.State == string(state.StateRunning) {
				t.Fatalf("frozen row after the refused thaw: %q, %v; it must not run", row.State, err)
			}
			if row.State != string(state.StateParked) {
				t.Fatalf("frozen row = %q, want parked", row.State)
			}
			if got := thawOutcomes(ops); got["no_capacity"] != 1 || got["success"] != 0 || got["fast"] != 0 {
				t.Fatalf("thaw outcomes = %v, want one no_capacity refusal", got)
			}
			if (*f.E()).Ledger().ResidentFor((*f.Ins()).ID) {
				t.Fatal("the parked VM kept its reservation")
			}
		})
	}
}
