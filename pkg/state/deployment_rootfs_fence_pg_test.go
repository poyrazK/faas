package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgSetDeploymentRootfsIfActiveFencesCancelledRow verifies the SQL CAS
// rather than only the MemStore mirror. Once cancellation commits, a late
// imaged publication must return a transition error and preserve the
// handoff metadata already on the row.
func TestPgSetDeploymentRootfsIfActiveFencesCancelledRow(t *testing.T) {
	store, ctx := pgStore(t)
	_, _, depID := seedPendingDeployPg(t, store, ctx, "rootfs-fence")

	if err := store.SetDeploymentRootfsIfActive(ctx, depID, "/handoff/image.tar", "builder/handoff", 11); err != nil {
		t.Fatalf("initial active rootfs stamp: %v", err)
	}
	if _, _, err := store.CancelDeploymentTx(ctx, depID, "operator:test", state.CancelReasonUser); err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}
	if err := store.SetDeploymentRootfsIfActive(ctx, depID, "/late/final.ext4", "apps/late.ext4", 22); !errors.Is(err, state.ErrInvalidStateTransition) {
		t.Fatalf("late rootfs stamp = %v, want ErrInvalidStateTransition", err)
	}
	got, err := store.DeploymentByID(ctx, depID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RootfsPath != "/handoff/image.tar" || got.RootfsKey != "builder/handoff" || got.RootfsBytes != 11 {
		t.Fatalf("cancelled row was mutated: path=%q key=%q bytes=%d", got.RootfsPath, got.RootfsKey, got.RootfsBytes)
	}
}
