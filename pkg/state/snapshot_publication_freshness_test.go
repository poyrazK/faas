package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testSnapshotPublicationFreshness(t *testing.T, store state.Store, nodeID string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "snapshot-fence-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-fence-" + uuid.NewString()[:8], RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:fence", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 256, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	// Rewaking the same instance advances its row's started_at. The delayed
	// notification must still be judged against the earlier captured value.
	time.Sleep(5 * time.Millisecond)
	if err := store.SetInstanceRuntime(ctx, old.ID, "test-ns", "192.0.2.10", 1000); err != nil {
		t.Fatal(err)
	}
	init := state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "old"), Tier: state.SnapshotTierInit}
	for name, source := range map[string]string{"old source": old.ID, "legacy source": "", "missing source": uuid.NewString()} {
		if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, init, source, old.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Errorf("%s publication = %v, want ErrSnapshotRuntimeStale", name, err)
		}
	}
	if _, err := store.LatestSnapshotForTier(ctx, dep.ID, state.SnapshotTierInit); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale capture became restorable: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	fresh, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 256, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	init.StorageKey = state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "fresh")
	if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, init, fresh.ID, fresh.StartedAt); err != nil {
		t.Fatalf("fresh source publication: %v", err)
	}
	got, err := store.LatestSnapshotForTier(ctx, dep.ID, state.SnapshotTierInit)
	if err != nil || got.StorageKey != init.StorageKey {
		t.Fatalf("fresh capture = (%+v, %v)", got, err)
	}
}

func TestMemSnapshotPublicationFreshness(t *testing.T) {
	testSnapshotPublicationFreshness(t, state.NewMemStore(), "test-node")
}

func TestPgSnapshotPublicationFreshness(t *testing.T) {
	store, ctx := pgStore(t)
	testSnapshotPublicationFreshness(t, store, resolveDefaultLocal(t, ctx, store))
}

func TestPgSnapshotPublicationWaitsForConfigStamp(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "snapshot-lock-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "snapshot-lock-" + uuid.NewString()[:8], RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:lock", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 256, resolveDefaultLocal(t, ctx, store), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	time.Sleep(5 * time.Millisecond)
	if _, err := tx.Exec(ctx, `insert into app_runtime_config_changes (app_id, changed_at) values ($1, clock_timestamp())`, app.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := store.PublishSnapshotIfRuntimeFresh(ctx, state.Snapshot{
			DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "racing"),
		}, source.ID, source.StartedAt)
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("publication bypassed uncommitted stamp: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("publication after stamp = %v, want stale", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication remained blocked after stamp committed")
	}
}
