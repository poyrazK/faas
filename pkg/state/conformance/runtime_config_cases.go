package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testRuntimeConfigChangeOrdersWithInstanceStart pins the contract schedd's
// park guard relies on (issue #3360): the runtime-config stamp and
// instances.started_at come from one clock, so an instance created before a
// change reports a start strictly before the stamp, and one created after
// reports a start no earlier than it. The stamp is per app.
func testRuntimeConfigChangeOrdersWithInstanceStart(t *testing.T, fx *Fixture) {
	create := func(label string) state.Instance {
		t.Helper()
		ins, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
			string(state.StateRunning), 256, fx.Node.ID, uuid.NewString())
		if err != nil {
			t.Fatalf("CreateInstance(%s): %v", label, err)
		}
		return ins
	}
	stamp := func() time.Time {
		t.Helper()
		if err := fx.Store.MarkAppRuntimeConfigChanged(fx.Ctx, fx.App.ID); err != nil {
			t.Fatalf("MarkAppRuntimeConfigChanged: %v", err)
		}
		changedAt, ok, err := fx.Store.AppRuntimeConfigChangedAt(fx.Ctx, fx.App.ID)
		if err != nil || !ok {
			t.Fatalf("AppRuntimeConfigChangedAt = (ok=%v, err=%v), want stamped", ok, err)
		}
		return changedAt
	}

	if _, ok, err := fx.Store.AppRuntimeConfigChangedAt(fx.Ctx, fx.App.ID); err != nil || ok {
		t.Fatalf("unchanged app = (ok=%v, err=%v), want no stamp", ok, err)
	}
	before := create("before")
	time.Sleep(2 * time.Millisecond)
	changedAt := stamp()
	if !changedAt.After(before.StartedAt) {
		t.Fatalf("stamp %v is not after the earlier instance start %v", changedAt, before.StartedAt)
	}
	time.Sleep(2 * time.Millisecond)
	after := create("after")
	if changedAt.After(after.StartedAt) {
		t.Fatalf("stamp %v is after the later instance start %v", changedAt, after.StartedAt)
	}
	if _, ok, err := fx.Store.AppRuntimeConfigChangedAt(fx.Ctx, uuid.NewString()); err != nil || ok {
		t.Fatalf("other app = (ok=%v, err=%v), want no stamp", ok, err)
	}
}

// Publication and a runtime-config stamp must agree on which side of the
// change a capture came from. This runs against both MemStore and PgStore;
// either implementation accepting the stale warm row would make a delayed
// snapshot_written notification restore an outdated environment.
func testSnapshotPublicationFencesRuntimeConfigChanges(t *testing.T, fx *Fixture) {
	createSource := func() state.Instance {
		t.Helper()
		ins, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
			string(state.StateRunning), 256, fx.Node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return ins
	}
	makeSnapshot := func(tier, generation string) state.Snapshot {
		return state.Snapshot{
			DeploymentID: fx.Deployment.ID,
			StorageKey:   state.SnapshotCaptureMemKey(fx.Deployment.ID, tier, generation),
			FCVersion:    "1.10.0", Tier: tier,
		}
	}
	before := createSource()
	init := makeSnapshot(state.SnapshotTierInit, "before")
	first, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, init, before.ID, before.StartedAt)
	if err != nil || first.ID == "" {
		t.Fatalf("fresh init publication = (%+v, %v)", first, err)
	}
	if _, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, init, before.ID, before.StartedAt); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate publication error = %v, want conflict", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := fx.Store.MarkAppRuntimeConfigChanged(fx.Ctx, fx.App.ID); err != nil {
		t.Fatal(err)
	}
	staleWarm := makeSnapshot(state.SnapshotTierWarm, "stale")
	if _, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, staleWarm, before.ID, before.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
		t.Fatalf("stale warm publication error = %v", err)
	}
	if _, err := fx.Store.LatestSnapshotForTier(fx.Ctx, fx.Deployment.ID, state.SnapshotTierWarm); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale warm row is visible: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	after := createSource()
	freshWarm := makeSnapshot(state.SnapshotTierWarm, "after")
	stored, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, freshWarm, after.ID, after.StartedAt)
	if err != nil || stored.ID == "" {
		t.Fatalf("fresh warm publication = (%+v, %v)", stored, err)
	}
	if got, err := fx.Store.LatestSnapshotForTier(fx.Ctx, fx.Deployment.ID, state.SnapshotTierWarm); err != nil || got.ID != stored.ID {
		t.Fatalf("latest warm snapshot = (%+v, %v), want %s", got, err, stored.ID)
	}
}
