// adr: 20261023181931 — amendment 3: a switch that keeps its release records no undone deployment.
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDatabaseSwitchThatKeepsItsReleaseRoundTrips(t *testing.T) {
	rollbackRegressionStores(t, func(t *testing.T, s state.Store) {
		a, app, target, current := checkedRollbackFixture(t, s, false)
		switches := s.(state.DatabaseSwitchStore)
		now := time.Now().UTC()
		base := state.DatabaseSwitch{AccountID: a.ID, AppID: app.ID, Scope: "default", SourceDatabaseID: uuid.NewString(),
			UndoneDeploymentIDs: []string{}, TargetName: "orders-0900", RestorePoint: now.Add(-time.Hour), CreatedAt: now}

		// Undoing nothing while changing the release is not a switch.
		moving := base
		moving.FromDeploymentID, moving.TargetDeploymentID = current.ID, target.ID
		if _, err := switches.CreateDatabaseSwitch(t.Context(), moving); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("switch undoing nothing to another release: %v, want ErrInvalidArgument", err)
		}

		keep := base
		keep.FromDeploymentID, keep.TargetDeploymentID = current.ID, current.ID
		created, err := switches.CreateDatabaseSwitch(t.Context(), keep)
		if err != nil {
			t.Fatal(err)
		}
		got, err := switches.DatabaseSwitch(t.Context(), a.ID, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.KeepsRelease() || len(got.UndoneDeploymentIDs) != 0 || got.TargetDeploymentID != current.ID || got.Phase != state.DatabaseSwitchRestoring {
			t.Fatalf("read back %+v", got)
		}
		undone, err := switches.ReleasesUndoneByDatabaseSwitches(t.Context(), app.ID, "default")
		if err != nil || len(undone) != 0 {
			t.Fatalf("a switch that keeps its release undid %v (%v)", undone, err)
		}
	})
}
