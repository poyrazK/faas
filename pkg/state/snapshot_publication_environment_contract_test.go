// adr: 568
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemSnapshotPublicationEnvironmentOwnership(t *testing.T) {
	testSnapshotPublicationEnvironmentOwnership(t, state.NewMemStore())
}

func testSnapshotPublicationEnvironmentOwnership(t *testing.T, store runtimeAppEnvTestStore) {
	t.Helper()
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	nodeID := runtimeSecretNodeForTest(t, store)
	source, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateParked), 256, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "owned")}
	if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, snapshot, "", time.Time{}); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
		t.Fatalf("stage accepted notification without source: %v", err)
	}
	other, err := store.CreateInstance(ctx, f.app.ID, f.deployments["other"].ID, string(state.StateParked), 256, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, snapshot, other.ID, other.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
		t.Fatalf("stage accepted sibling source: %v", err)
	}
	first, err := store.PublishSnapshotIfRuntimeFresh(ctx, snapshot, source.ID, source.StartedAt)
	if err != nil {
		t.Fatalf("owned stage publication: %v", err)
	}
	if err := store.MarkSnapshotStale(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "replacement"); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	for _, tier := range []string{state.SnapshotTierInit, state.SnapshotTierWarm} {
		snapshot.Tier, snapshot.StorageKey = tier, state.SnapshotCaptureMemKey(dep.ID, tier, "delayed")
		if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, snapshot, source.ID, source.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("old stage published %s capture into replacement lifetime: %v", tier, err)
		}
		if _, err := store.LatestSnapshotForTier(ctx, dep.ID, tier); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("old %s capture became restorable: %v", tier, err)
		}
	}
	for _, current := range []state.Deployment{replacement, f.deployments["production"]} {
		instance, err := store.CreateInstance(ctx, f.app.ID, current.ID, string(state.StateParked), 256, nodeID, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, state.Snapshot{DeploymentID: current.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(current.ID, state.SnapshotTierInit, "current")}, instance.ID, instance.StartedAt); err != nil {
			t.Fatalf("current %s lifetime rejected valid capture: %v", current.Scope, err)
		}
	}
	values, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, f.app.ID, replacement.ID)
	if err != nil || len(values.Values) != 1 || values.Values[0].Value != "replacement" {
		t.Fatalf("publication modified replacement configuration: %+v %v", values, err)
	}
}

func TestMemSnapshotPublicationRequiresExactSourceStart(t *testing.T) {
	testSnapshotPublicationRequiresExactSourceStart(t, state.NewMemStore())
}

func testSnapshotPublicationRequiresExactSourceStart(t *testing.T, store runtimeAppEnvTestStore) {
	t.Helper()
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	source, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateParked), 256, runtimeSecretNodeForTest(t, store), "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := store.SetInstanceRuntime(ctx, source.ID, "new-runtime", "192.0.2.1", 20001); err != nil {
		t.Fatal(err)
	}
	current, err := store.InstanceByID(ctx, source.ID)
	if err != nil || !current.StartedAt.After(source.StartedAt) {
		t.Fatalf("source did not advance: %+v %v", current, err)
	}
	snapshot := state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "previous-runtime")}
	if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, snapshot, source.ID, source.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
		t.Fatalf("old runtime published without a config stamp: %v", err)
	}
	if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, snapshot, current.ID, current.StartedAt); err != nil {
		t.Fatalf("current runtime publication: %v", err)
	}
}
