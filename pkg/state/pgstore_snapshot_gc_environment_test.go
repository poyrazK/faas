//go:build !no_pg

// adr: 569
package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestPgSnapshotGCEnvironmentMetadata(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testSnapshotGCEnvironmentMetadata(t, store)
}

func TestPgSnapshotGCPendingIgnoresFutureTimestamp(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	testSnapshotGCPendingIgnoresFutureTimestamp(t, store, func(id string, createdAt time.Time) error {
		_, err := pool.Exec(ctx, `UPDATE snapshots SET created_at=$2 WHERE id=$1`, id, createdAt)
		return err
	})
}

func TestPgSnapshotGCLostPinsAndLegacyOwnership(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	snapshot, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "owner")})
	if err != nil {
		t.Fatal(err)
	}
	var specID, environmentID string
	if err := pool.QueryRow(ctx, `SELECT pin.spec_id::text, owner.environment_id::text FROM project_environment_workload_deployment_specs pin JOIN deployment_runtime_environment_owners owner USING(deployment_id) WHERE pin.deployment_id=$1`, dep.ID).Scan(&specID, &environmentID); err != nil {
		t.Fatal(err)
	}
	read := func(wantInvalid bool, wantEnvironmentID string) {
		t.Helper()
		rows, err := store.ListSnapshotsForGC(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID != snapshot.ID {
				continue
			}
			if row.RuntimeOwnerInvalid != wantInvalid || row.EnvironmentID != wantEnvironmentID {
				t.Fatalf("GC downgraded missing owner/pin: %+v", row)
			}
			return
		}
		t.Fatal("GC hid snapshot ownership debt")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM project_environment_workload_deployment_specs WHERE deployment_id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	read(true, environmentID)
	if _, err := pool.Exec(ctx, `INSERT INTO project_environment_workload_deployment_specs(deployment_id,spec_id) VALUES ($1,$2)`, dep.ID, specID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM deployment_runtime_environment_owners WHERE deployment_id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	read(true, "")
	if _, err := pool.Exec(ctx, `DELETE FROM project_environment_workload_deployment_specs WHERE deployment_id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	read(false, environmentID)
	if _, err := pool.Exec(ctx, `UPDATE deployments SET created_at=(SELECT created_at-interval '1 second' FROM project_environments WHERE id=$2) WHERE id=$1`, dep.ID, environmentID); err != nil {
		t.Fatal(err)
	}
	read(true, "")
}
