// adr: 20261020085706 — amendment 13: an outpaced release never runs its
// release command.
package conformance

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// testSupersedeOutpacedRelease pins that a release in its release phase is
// superseded once a newer revision of its app and scope exists, and that the
// newest release, and one in another scope, are left alone.
func testSupersedeOutpacedRelease(t *testing.T, fx *Fixture) {
	store, ok := fx.Store.(state.OutpacedReleaseStore)
	if !ok {
		t.Skip("store cannot supersede outpaced releases")
	}
	releasePhase := func(scope string) state.Deployment {
		t.Helper()
		d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: fx.App.ID, Kind: state.DeploymentKindImage,
			Scope: scope, ImageDigest: "sha256:" + scope, ReleaseCommand: []string{"bin/migrate"}})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		for _, status := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
			if err := fx.Store.UpdateDeploymentStatus(fx.Ctx, d.ID, status, ""); err != nil {
				t.Fatalf("UpdateDeploymentStatus(%s): %v", status, err)
			}
		}
		return d
	}
	supersede := func(id string) bool {
		t.Helper()
		got, err := store.SupersedeOutpacedRelease(fx.Ctx, id)
		if err != nil {
			t.Fatalf("SupersedeOutpacedRelease(%s): %v", id, err)
		}
		return got
	}

	older := releasePhase("production")
	other := releasePhase("staging")
	newer := releasePhase("production")
	if supersede(newer.ID) {
		t.Fatal("the newest release was superseded")
	}
	if supersede(other.ID) {
		t.Fatal("a release in another scope was superseded")
	}
	if !supersede(older.ID) {
		t.Fatal("an outpaced release was not superseded")
	}
	row, err := fx.Store.DeploymentByID(fx.Ctx, older.ID)
	if err != nil || row.Status != state.DeploySuperseded {
		t.Fatalf("outpaced release = %q (err=%v), want superseded", row.Status, err)
	}
	if supersede(older.ID) {
		t.Fatal("a superseded release was superseded again")
	}
}
