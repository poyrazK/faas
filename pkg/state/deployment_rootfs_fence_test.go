package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

// TestMemStoreSetDeploymentRootfsIfActiveFencesTerminalRows pins the same
// status predicate used by PgStore. A late imaged write must not mutate a
// cancelled, failed, superseded, or live deployment, while the pre-live
// states remain writable for the build pipeline.
func TestMemStoreSetDeploymentRootfsIfActiveFencesTerminalRows(t *testing.T) {
	ctx := context.Background()
	for _, status := range []state.DeploymentStatus{
		state.DeployPending, state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting,
		state.DeployCancelled, state.DeployFailed, state.DeploySuperseded, state.DeployLive,
	} {
		t.Run(string(status), func(t *testing.T) {
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "rootfs-fence-"+string(status)+"@example.com", "pro")
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rootfs-fence-" + string(status)})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: status})
			if err != nil {
				t.Fatal(err)
			}
			writeErr := store.SetDeploymentRootfsIfActive(ctx, dep.ID, "/late.ext4", "apps/late.ext4", 7)
			if status.IsTerminal() || status == state.DeployLive {
				if !errors.Is(writeErr, state.ErrInvalidStateTransition) {
					t.Fatalf("write error = %v, want ErrInvalidStateTransition", writeErr)
				}
				got, getErr := store.DeploymentByID(ctx, dep.ID)
				if getErr != nil {
					t.Fatal(getErr)
				}
				if got.RootfsPath != "" || got.RootfsKey != "" || got.RootfsBytes != 0 {
					t.Fatalf("terminal row mutated: %+v", got)
				}
				return
			}
			if writeErr != nil {
				t.Fatalf("active write: %v", writeErr)
			}
			got, getErr := store.DeploymentByID(ctx, dep.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if got.RootfsPath != "/late.ext4" || got.RootfsKey != "apps/late.ext4" || got.RootfsBytes != 7 {
				t.Fatalf("active row not stamped: %+v", got)
			}
		})
	}
}
