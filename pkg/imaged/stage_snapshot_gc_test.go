// adr: 566
package imaged

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestSnapshotGCRetainsEachEnvironmentRollbackWindow(t *testing.T) {
	var rows []state.SnapshotForGC
	base := time.Now().UTC().Add(-time.Hour)
	for environmentIndex, environment := range []string{"production", "stage", "other"} {
		for generation := 0; generation < 4; generation++ {
			dep := fmt.Sprintf("%s-%d", environment, generation)
			for _, tier := range []string{state.SnapshotTierInit, state.SnapshotTierWarm} {
				snapshot := row(dep+"-"+tier, "app", dep, "account", "app", tier, true, base.Add(time.Duration(environmentIndex*10+generation)*time.Minute), 1, 1)
				snapshot.EnvironmentID, snapshot.Scope = "lifetime-"+environment, environment
				rows = append(rows, snapshot)
			}
		}
	}
	want := []string{"production-0-init", "production-0-warm", "stage-0-init", "stage-0-warm", "other-0-init", "other-0-warm"}
	if got := collectIDs(perAppKeepRollbackWindow(rows, 3)); !sortedStringsEqual(got, want) {
		t.Fatalf("stage activity displaced sibling rollback snapshots: %v", got)
	}
	pressure := evictOldestFromHeaviestAccount(rows)
	if len(pressure) != 1 || pressure[0].DeploymentID != "production-0" || pressure[0].AppID != "app" || pressure[0].AccountID != "account" {
		t.Fatalf("pressure lost generation protection or metadata: %+v", pressure)
	}
	if got := collectIDs(perAppKeepTierFloor(rows)); !sortedStringsEqual(got, []string{"production-0-init", "production-0-warm", "production-1-init", "production-1-warm", "stage-0-init", "stage-0-warm", "stage-1-init", "stage-1-warm", "other-0-init", "other-0-warm", "other-1-init", "other-1-warm"}) {
		t.Fatalf("tier floor crossed environments: %v", got)
	}
}

func TestSnapshotGCUsesPinnedPolicyAndExcludesLostOwners(t *testing.T) {
	base := time.Now().UTC()
	var rows []state.SnapshotForGC
	for i := 0; i < 3; i++ {
		dep := fmt.Sprintf("dep-%d", i)
		for _, tier := range []string{state.SnapshotTierInit, state.SnapshotTierWarm} {
			snapshot := row(dep+"-"+tier, "app", dep, "account", "app", tier, i == 1, base.Add(time.Duration(i)*time.Minute), 1, 1)
			snapshot.Scope = "stage"
			snapshot.EnvironmentID = "original"
			rows = append(rows, snapshot)
		}
	}
	orphan := row("orphan", "app", "deleted-owner", "account", "app", state.SnapshotTierInit, true, base.Add(4*time.Minute), 1, 1)
	orphan.EnvironmentID, orphan.Scope, orphan.RuntimeOwnerInvalid = "original", "stage", true
	rows = append(rows, orphan)
	want := []string{"dep-0-warm", "dep-2-warm", "orphan"}
	if got := collectIDs(perAppKeepRollbackWindow(rows, 3)); !sortedStringsEqual(got, want) {
		t.Fatalf("pinned policy or cleanup debt consumed rollback slot: %v", got)
	}
	candidates := perAppRollbackEvictionCandidates(rows, 3)
	var ids []string
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	if !sortedStringsEqual(ids, want) {
		t.Fatalf("pressure policy drifted from regular GC: %v", ids)
	}
}

func TestSnapshotGCLegacyProductionScopeSharesWindow(t *testing.T) {
	var rows []state.SnapshotForGC
	for i, scope := range []string{"", "default", "production", "default"} {
		snapshot := row(fmt.Sprintf("snap-%d", i), "app", fmt.Sprintf("dep-%d", i), "account", "app", state.SnapshotTierInit, false, time.Unix(int64(i), 0), 1, 1)
		snapshot.Scope = scope
		rows = append(rows, snapshot)
	}
	if got := collectIDs(perAppKeepRollbackWindow(rows, 3)); !sortedStringsEqual(got, []string{"snap-0"}) {
		t.Fatalf("legacy production split windows: %v", got)
	}
}

func TestSnapshotGCSharedPhysicalLayerSurvivesUntilLastStageReference(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "shared-gc@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "shared-gc", RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	key := "apps/source/copied-layer.ext4"
	deployments := make([]state.Deployment, 0, 2)
	for _, scope := range []string{"production", "stage"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "", key, 1024); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "shared")}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		deployments = append(deployments, dep)
	}
	backend := mustLocalStorage(t, t.TempDir())
	if err := backend.Put(ctx, key, strings.NewReader("shared immutable image")); err != nil {
		t.Fatal(err)
	}
	handler := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(backend)
	loop := &Loop{store: store, log: silentLogger(), handler: handler}
	rows, err := store.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byDeployment := map[string]state.SnapshotForGC{}
	for _, snapshot := range rows {
		byDeployment[snapshot.DeploymentID] = snapshot
	}
	first := targetForSnapshot(byDeployment[deployments[0].ID])
	if first.DeploymentRootfsKey != key {
		t.Fatalf("projection lost physical key: %+v", first)
	}
	if err := loop.deleteSnapshotsAndFiles(ctx, []deleteTarget{first}); err != nil {
		t.Fatal(err)
	}
	reader, err := backend.Get(ctx, key)
	if err != nil {
		t.Fatalf("production GC deleted stage image: %v", err)
	}
	_ = reader.Close()
	if err := loop.deleteSnapshotsAndFiles(ctx, []deleteTarget{targetForSnapshot(byDeployment[deployments[1].ID])}); err != nil {
		t.Fatal(err)
	}
	reader, err = backend.Get(ctx, key)
	if reader != nil {
		_ = reader.Close()
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("last reference left physical layer: %v", err)
	}
}

func TestDeletedNotificationRetainsActiveAppArtifacts(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "active-gc@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "active-gc", RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	backend := mustLocalStorage(t, t.TempDir())
	key := "apps/" + app.Slug + "/" + dep.ID + ".ext4"
	if err := backend.Put(ctx, key, strings.NewReader("active image")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "active")})
	if err != nil {
		t.Fatal(err)
	}
	snapshotKeys := []string{snapshot.StorageKey, state.SnapshotVMStateKey(snapshot), state.SnapshotDriveKey(snapshot), state.SnapMemKey(dep.ID), state.SnapVMStateKey(dep.ID)}
	for _, snapshotKey := range snapshotKeys {
		if err := backend.Put(ctx, snapshotKey, strings.NewReader("active snapshot")); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(backend)
	handler.HandleNotification(ctx, db.Notification{Channel: db.NotifyAppChanged, Payload: fmt.Sprintf(`{"kind":"deleted","slug":%q,"app_id":%q}`, app.Slug, app.ID)})
	reader, err := backend.Get(ctx, key)
	if err != nil {
		t.Fatalf("notification hint deleted active app image: %v", err)
	}
	_ = reader.Close()
	for _, snapshotKey := range snapshotKeys {
		reader, err := backend.Get(ctx, snapshotKey)
		if err != nil {
			t.Fatalf("notification hint deleted active snapshot %s: %v", snapshotKey, err)
		}
		_ = reader.Close()
	}
}

func TestSnapshotGCRetainsSeparateLifetimesWithSameStageSlug(t *testing.T) {
	var rows []state.SnapshotForGC
	for lifetimeIndex, lifetime := range []string{"original", "replacement"} {
		for generation := 0; generation < 4; generation++ {
			id := fmt.Sprintf("%s-%d", lifetime, generation)
			snapshot := row(id, "app", id, "account", "app", state.SnapshotTierInit, false, time.Unix(int64(lifetimeIndex*10+generation), 0), 1, 1)
			snapshot.Scope, snapshot.EnvironmentID = "stage", lifetime
			rows = append(rows, snapshot)
		}
	}
	if got := collectIDs(perAppKeepRollbackWindow(rows, 3)); !sortedStringsEqual(got, []string{"original-0", "replacement-0"}) {
		t.Fatalf("stage slug merged original environment lifetimes: %v", got)
	}
}
