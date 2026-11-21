// adr: 20261115113000 — a park that finds no init snapshot on its node does
// not capture the used instance; the refresher recaptures a clean boot.

package sched_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// usedInstance seeds an app with a live deployment and no snapshot, and a
// RUNNING instance on node, as a cold-boot wake leaves it.
func (f *refreshFixture) usedInstance(t *testing.T, node string) (state.App, state.Deployment, state.Instance) {
	t.Helper()
	acct, err := f.store.CreateAccount(f.ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := f.store.CreateApp(f.ctx, state.App{AccountID: acct.ID, Slug: "app-" + uuid.NewString()[:8], RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	ins, err := f.store.CreateInstanceWithMode(f.ctx, app.ID, dep.ID, string(state.StateRunning), app.RAMMB, node, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode: %v", err)
	}
	return app, dep, ins
}

func TestCleanRecapture_ParkDestroysInsteadOfCapturing(t *testing.T) {
	f := newRefreshFixture(t)
	_, _, ins := f.usedInstance(t, f.nodeA.ID)

	if err := f.engine.Park(f.ctx, ins.ID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if _, snaps, destroys := f.boots.XBootCounts(); snaps != 0 || destroys != 1 {
		t.Fatalf("park snapshots %d, destroys %d; want the used instance destroyed, not captured", snaps, destroys)
	}
	got, err := f.store.InstanceByID(f.ctx, ins.ID)
	if err != nil || got.State != string(state.StateStopped) {
		t.Fatalf("instance = %s (err %v), want stopped", got.State, err)
	}
	if n := f.refresher.PendingCleanRecaptures(); n != 1 {
		t.Fatalf("pending recaptures = %d, want 1", n)
	}

	f.refresher.DrainCleanRecaptures(f.ctx)
	if cold, snaps, _ := f.boots.XBootCounts(); cold != 1 || snaps != 1 {
		t.Fatalf("recapture cold boots %d, snapshots %d; want one clean boot captured", cold, snaps)
	}
	if n := f.refresher.PendingCleanRecaptures(); n != 0 {
		t.Fatalf("pending recaptures after drain = %d, want 0", n)
	}
	// Invariant 4: the parked recapture holds no resident RAM.
	if ram := f.refresher.XResidentRAMForNode(f.nodeA.ID); ram != 0 {
		t.Fatalf("node a resident RAM after the recapture parked = %d MB, want 0", ram)
	}
}

func TestCleanRecapture_OffCapturesAtPark(t *testing.T) {
	f := newRefreshFixture(t)
	if err := f.refresher.SetCleanRecapture(api.SnapshotCleanRecaptureOff); err != nil {
		t.Fatal(err)
	}
	_, _, ins := f.usedInstance(t, f.nodeA.ID)
	if err := f.engine.Park(f.ctx, ins.ID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if _, snaps, _ := f.boots.XBootCounts(); snaps != 1 {
		t.Fatalf("park snapshots = %d, want the off switch to capture as before", snaps)
	}
	if n := f.refresher.PendingCleanRecaptures(); n != 0 {
		t.Fatalf("pending recaptures = %d, want none when off", n)
	}
}

func TestCleanRecapture_ReusesASnapshotOnTheNode(t *testing.T) {
	f := newRefreshFixture(t)
	app, _ := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
	deps, err := f.store.LiveDeployments(f.ctx, app.ID)
	if err != nil || len(deps) != 1 {
		t.Fatalf("LiveDeployments = %v, %v", deps, err)
	}
	ins, err := f.store.CreateInstanceWithMode(f.ctx, app.ID, deps[0].ID, string(state.StateRunning), app.RAMMB, f.nodeA.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Park(f.ctx, ins.ID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if n := f.refresher.PendingCleanRecaptures(); n != 0 {
		t.Fatalf("pending recaptures = %d, want reuse of the node's snapshot", n)
	}
	if _, snaps, _ := f.boots.XBootCounts(); snaps != 0 {
		t.Fatalf("park snapshots = %d, want reuse", snaps)
	}
}

func TestCleanRecapture_DefersWithoutHeadroom(t *testing.T) {
	f := newRefreshFixture(t)
	_, _, ins := f.usedInstance(t, f.nodeA.ID)
	if err := f.engine.Park(f.ctx, ins.ID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	sched.XFillLedger(t, f.refresher, f.nodeA.ID, api.SnapshotRefreshMaxResidencyPercent)
	f.refresher.DrainCleanRecaptures(f.ctx)
	if cold, _, _ := f.boots.XBootCounts(); cold != 0 {
		t.Fatalf("booted %d recaptures without headroom", cold)
	}
	if n := f.refresher.PendingCleanRecaptures(); n != 1 {
		t.Fatalf("pending recaptures = %d, want the deferred request kept", n)
	}
}

func TestCleanRecapture_SettingChecksItsValues(t *testing.T) {
	f := newRefreshFixture(t)
	if err := f.refresher.SetCleanRecapture("shadow"); err == nil {
		t.Fatal("shadow accepted")
	}
	if !f.refresher.CleanRecaptureEnabled() {
		t.Fatalf("default = off, want %s", api.DefaultSnapshotCleanRecapture)
	}
}

// adr: 20261116192126 — a managed cache's memory is its data. Its park
// captures the running VM whether or not a reusable init snapshot exists, and
// never queues a clean recapture.
func TestManagedCacheParkCapturesMemory(t *testing.T) {
	for _, withSnapshot := range []bool{true, false} {
		f := newRefreshFixture(t)
		var ins state.Instance
		var app state.App
		if withSnapshot {
			app, _ = f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{})
			deps, err := f.store.LiveDeployments(f.ctx, app.ID)
			if err != nil || len(deps) != 1 {
				t.Fatalf("LiveDeployments = %v, %v", deps, err)
			}
			ins, err = f.store.CreateInstanceWithMode(f.ctx, app.ID, deps[0].ID, string(state.StateRunning), app.RAMMB, f.nodeA.ID, uuid.NewString(), string(state.InstanceModeNormal))
			if err != nil {
				t.Fatal(err)
			}
		} else {
			app, _, ins = f.usedInstance(t, f.nodeA.ID)
		}
		if _, err := f.store.CreateManagedValkeyCache(f.ctx, state.ManagedValkeyCache{
			AppID: app.ID, AccountID: app.AccountID, MemoryMB: 128, EvictionPolicy: "allkeys-lru",
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.engine.Park(f.ctx, ins.ID); err != nil {
			t.Fatalf("withSnapshot=%v: Park: %v", withSnapshot, err)
		}
		if _, snaps, _ := f.boots.XBootCounts(); snaps != 1 {
			t.Fatalf("withSnapshot=%v: park snapshots = %d, want the cache's memory captured", withSnapshot, snaps)
		}
		if n := f.refresher.PendingCleanRecaptures(); n != 0 {
			t.Fatalf("withSnapshot=%v: pending recaptures = %d, want none for a cache", withSnapshot, n)
		}
	}
}
