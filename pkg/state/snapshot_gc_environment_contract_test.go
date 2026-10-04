// adr: 531
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemSnapshotGCEnvironmentMetadata(t *testing.T) {
	testSnapshotGCEnvironmentMetadata(t, state.NewMemStore())
}

func testSnapshotGCEnvironmentMetadata(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	original := f.deployments["stage"]
	environment, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "stage")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmSnapshotEnabled = true
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 1, settings); err != nil {
		t.Fatal(err)
	}
	warmDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmSnapshotEnabled = false
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 2, settings); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{WarmSnapshotEnabled: &enabled, SetWarmSnapshotEnabled: true}); err != nil {
		t.Fatal(err)
	}
	physicalKey := "apps/shared-source/immutable-layer.ext4"
	snapshots := map[string]state.Snapshot{}
	for _, dep := range []state.Deployment{original, warmDeployment, f.deployments["production"], f.deployments["other"]} {
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "", physicalKey, 1024); err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "gc"), CreatedAt: time.Now().UTC().Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		snapshots[dep.ID] = snapshot
	}
	rows, err := store.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byDeployment := map[string]state.SnapshotForGC{}
	for _, row := range rows {
		byDeployment[row.DeploymentID] = row
	}
	for _, dep := range []state.Deployment{original, warmDeployment} {
		got := byDeployment[dep.ID]
		if got.Scope != "stage" || got.EnvironmentID != environment.ID || got.RuntimeOwnerInvalid || got.DeploymentRootfsKey != physicalKey || got.AppWarmSnapshotEnabled != (dep.ID == warmDeployment.ID) {
			t.Fatalf("GC adopted current head/shared app for deployment %s: %+v", dep.ID, got)
		}
		if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkSnapshotStale(ctx, snapshots[dep.ID].ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	selectors := []struct {
		name string
		read func() ([]state.SnapshotForGC, error)
	}{
		{"active", func() ([]state.SnapshotForGC, error) { return store.ListSnapshotsForGC(ctx) }},
		{"stale", func() ([]state.SnapshotForGC, error) { return store.ListSnapshotsStaleOlderThan(ctx, 0) }},
		{"pending", func() ([]state.SnapshotForGC, error) { return store.ListSnapshotsPendingDelete(ctx) }},
	}
	if _, err := store.MarkOldSnapshotsStale(ctx, []string{snapshots[original.ID].ID, snapshots[warmDeployment.ID].ID}); err != nil {
		t.Fatal(err)
	}
	for _, selector := range selectors {
		rows, err := selector.read()
		if err != nil {
			t.Fatalf("%s: %v", selector.name, err)
		}
		found := 0
		for _, row := range rows {
			if row.DeploymentID != original.ID && row.DeploymentID != warmDeployment.ID {
				continue
			}
			found++
			if row.EnvironmentID != environment.ID || row.EnvironmentID == replacement.ID || !row.RuntimeOwnerInvalid || row.DeploymentRootfsKey != physicalKey || !row.DeletePending {
				t.Fatalf("%s lost original environment or physical key: %+v", selector.name, row)
			}
		}
		if found != 2 {
			t.Fatalf("%s hid orphaned stage snapshots: %+v", selector.name, rows)
		}
	}
	rows, err = store.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if (row.DeploymentID == f.deployments["production"].ID || row.DeploymentID == f.deployments["other"].ID) && row.RuntimeOwnerInvalid {
			t.Fatalf("stage deletion invalidated sibling: %+v", row)
		}
	}
}

func TestMemSnapshotGCPendingIgnoresFutureTimestamp(t *testing.T) {
	testSnapshotGCPendingIgnoresFutureTimestamp(t, state.NewMemStore(), nil)
}

func testSnapshotGCPendingIgnoresFutureTimestamp(t *testing.T, store runtimeAppEnvTestStore, setCreatedAt func(string, time.Time) error) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	snapshot, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "future"), CreatedAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if setCreatedAt != nil {
		if err := setCreatedAt(snapshot.ID, time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.MarkOldSnapshotsStale(ctx, []string{snapshot.ID}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListSnapshotsPendingDelete(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != snapshot.ID || !pending[0].DeletePending {
		t.Fatalf("future tombstone disappeared: %+v %v", pending, err)
	}
	expired, err := store.ListSnapshotsStaleOlderThan(ctx, 0)
	if err != nil || len(expired) != 0 {
		t.Fatalf("future snapshot aged out: %+v %v", expired, err)
	}
}
