// adr: 051 — characterization boot: observed workload classification.
package conformance

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testObservedWorkloadClass covers the unproved path both stores share: an
// instance without a published config proof records the observed class,
// bumps the scaling-policy revision only on a change, and reports no restamp.
// Unknown, foreign and empty inputs are refused.
func testObservedWorkloadClass(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.RuntimeInstancePublicationStore)
	if !ok {
		t.Fatal("store does not implement RuntimeInstancePublicationStore")
	}
	before, err := fx.Store.AppByID(fx.Ctx, fx.App.ID)
	if err != nil {
		t.Fatal(err)
	}
	observed := state.WorkloadClassWorker
	if before.WorkloadClass == observed {
		observed = state.WorkloadClassJob
	}
	instanceID := uuid.NewString()
	restamped, err := store.RecordObservedWorkloadClass(fx.Ctx, fx.Account.ID, fx.App.ID, instanceID, observed)
	if err != nil || restamped {
		t.Fatalf("record without a proof: restamped=%v err=%v", restamped, err)
	}
	after, err := fx.Store.AppByID(fx.Ctx, fx.App.ID)
	if err != nil || after.WorkloadClass != observed || after.ScalingPolicyRevision != before.ScalingPolicyRevision+1 {
		t.Fatalf("after change: class=%q revision=%d (before %d) err=%v", after.WorkloadClass, after.ScalingPolicyRevision, before.ScalingPolicyRevision, err)
	}
	if restamped, err := store.RecordObservedWorkloadClass(fx.Ctx, fx.Account.ID, fx.App.ID, instanceID, observed); err != nil || restamped {
		t.Fatalf("repeat: restamped=%v err=%v", restamped, err)
	}
	again, err := fx.Store.AppByID(fx.Ctx, fx.App.ID)
	if err != nil || again.ScalingPolicyRevision != after.ScalingPolicyRevision {
		t.Fatalf("an unchanged class moved the revision: %d -> %d (%v)", after.ScalingPolicyRevision, again.ScalingPolicyRevision, err)
	}
	if _, err := store.RecordObservedWorkloadClass(fx.Ctx, fx.Account.ID, fx.App.ID, instanceID, ""); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("empty class error = %v, want invalid argument", err)
	}
	for name, ids := range map[string][2]string{
		"unknown app":     {fx.Account.ID, uuid.NewString()},
		"foreign account": {uuid.NewString(), fx.App.ID},
	} {
		if _, err := store.RecordObservedWorkloadClass(fx.Ctx, ids[0], ids[1], instanceID, observed); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("%s error = %v, want not found", name, err)
		}
	}
}
