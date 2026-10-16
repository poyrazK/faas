//go:build !no_pg

// adr: 590
package apidserver

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"strings"
)

func incompleteCheckpointCleanupFixture(t *testing.T, phase string) (*cloneCoordinatorFixture, *checkpointReleaseLostReplyStore, *checkpointReleaseTestDriver) {
	t.Helper()
	f, store, d, _ := checkpointReleaseFixture(t)
	var err error
	if phase == "unknown-release" {
		d.loseRelease = true
		f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, d, 10)
		if !errors.Is(err, objectstorage.ErrUnavailable) {
			t.Fatal("unknown original release", err)
		}
	} else {
		f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, d, 10)
		if err != nil {
			t.Fatal(err)
		}
		if phase != "adopted" {
			p, _, _, err := f.srv.cloneCommonCheckpointPlan(t.Context(), f.lease, statetest.NewPg(store))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.PgStore.ReserveProjectEnvironmentCloneCheckpointRelease(t.Context(), f.lease, p, d.adoption.Receipt); err != nil {
				t.Fatal(err)
			}
			if phase == "requested" {
				if _, _, err = store.ClaimProjectEnvironmentCloneCheckpointRelease(t.Context(), f.lease); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	compensateCheckpointObjectFixture(t, &f)
	return &f, store, d
}

func TestPGCloneCheckpointIncompleteCleanupRecovery(t *testing.T) {
	for _, phase := range []string{"adopted", "planned", "requested", "unknown-release"} {
		t.Run(phase, func(t *testing.T) {
			f, store, release := incompleteCheckpointCleanupFixture(t, phase)
			original := append([]state.ProjectEnvironmentCloneResource(nil), f.lease.Operation.Resources...)
			var releaseState string
			if phase != "adopted" {
				if err := f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_releases where operation_id=$1", f.lease.Operation.ID).Scan(&releaseState); err != nil {
					t.Fatal(err)
				}
			}
			originalReleases := release.releases
			d := &checkpointObjectRetirementTestDriver{t: t, f: f, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation{}}
			var err error
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal("incomplete capture cleanup", err)
			}
			stale := f.lease
			reclaimCommonCheckpointWorker(t, f)
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal("cleanup handoff/replay", err)
			}
			var id string
			for _, r := range original {
				if r.Kind == "object_storage" {
					id = r.SourceID
					break
				}
			}
			if err = store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), stale, id); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale cleanup", err)
			}
			var completed, deleted, held, snapshots int
			for _, c := range []struct {
				sql string
				n   *int
			}{
				{"select count(*) from project_environment_clone_checkpoint_unused_object_reservations where operation_id=$1", &completed},
				{"select count(*) from object_buckets where environment_clone_operation_id=$1 and state='deleted'", &deleted},
				{"select count(*) from project_environment_clone_configuration_guards where operation_id=$1 and state='held'", &held},
				{"select count(*) from project_environment_clone_postgres_snapshots where operation_id=$1 and state='retained'", &snapshots},
			} {
				if err = f.pool.QueryRow(t.Context(), c.sql, f.lease.Operation.ID).Scan(c.n); err != nil {
					t.Fatal(err)
				}
			}
			if completed != 2 || deleted != 2 || held != 1 || snapshots != 1 || d.calls != 0 || release.releases != originalReleases || !reflect.DeepEqual(original, f.lease.Operation.Resources) {
				t.Fatal("cleanup changed source authority", completed, deleted, held, snapshots, d.calls, release.releases)
			}
			if phase != "adopted" {
				var actual string
				if err = f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_releases where operation_id=$1", f.lease.Operation.ID).Scan(&actual); err != nil || actual != releaseState {
					t.Fatal("cleanup changed original release", actual, err)
				}
			}
		})
	}
}

func TestPGCloneCheckpointIncompleteCleanupProvenance(t *testing.T) {
	for _, failure := range []string{"receipt-hash", "adoption-hash", "snapshot", "manifest", "configuration-owner", "configuration-generation", "missing-adoption", "missing-manifest", "missing-snapshot"} {
		t.Run(failure, func(t *testing.T) {
			f, store, _ := incompleteCheckpointCleanupFixture(t, "adopted")
			var id string
			for _, r := range f.lease.Operation.Resources {
				if r.Kind == "object_storage" {
					id = r.SourceID
					break
				}
			}
			var sql string
			switch failure {
			case "receipt-hash":
				sql = "update project_environment_clone_operations set resources=jsonb_set(resources,'{1,source_version}',to_jsonb(repeat('f',64))) where id=$1"
			case "adoption-hash":
				sql = "update project_environment_clone_operations set resources=jsonb_set(resources,ARRAY[(jsonb_array_length(resources)-1)::text,'source_version'],to_jsonb(repeat('f',64))) where id=$1"
			case "snapshot":
				sql = "update project_environment_clone_postgres_snapshots set provider_snapshot_id='foreign-snapshot' where operation_id=$1"
			case "manifest":
				sql = "update project_environment_clone_object_manifests set manifest_hash=repeat('f',64) where operation_id=$1"
			case "configuration-owner":
				sql = "update project_environment_clone_configuration_guards set operation_id=null,state='open',source_environment='',source_revision_hash='',held_at=null where operation_id=$1"
			case "configuration-generation":
				sql = "update project_environment_clone_configuration_guards set generation=generation+1 where operation_id=$1"
			case "missing-adoption":
				sql = "update project_environment_clone_operations set resources=resources-(jsonb_array_length(resources)-1) where id=$1"
			case "missing-manifest":
				sql = "delete from project_environment_clone_object_manifests where operation_id=$1"
			case "missing-snapshot":
				sql = "delete from project_environment_clone_postgres_snapshots where operation_id=$1"
			}
			if _, err := f.pool.Exec(t.Context(), sql, f.lease.Operation.ID); err != nil {
				t.Fatal("tamper fixture", err)
			}
			if err := store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
				t.Fatal("corrupt adoption authorized cleanup", err)
			}
			var completed int
			if err := f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_unused_object_reservations where operation_id=$1", f.lease.Operation.ID).Scan(&completed); err != nil || completed != 0 {
				t.Fatal("corrupt cleanup committed", completed, err)
			}
		})
	}
}

func TestPGCloneCheckpointIncompleteCleanupReleaseRewind(t *testing.T) {
	f, store, _ := checkpointTargetFixture(t)
	compensateCheckpointObjectFixture(t, f)
	var id string
	for _, r := range f.lease.Operation.Resources {
		if r.Kind == "object_storage" {
			id = r.SourceID
			break
		}
	}
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_releases set state='planned',requested_at=null,observation_sha256=null,released_at=null where operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("rewound release authorized cleanup", err)
	}
}

func TestPGCloneCheckpointIncompleteCleanupGuard(t *testing.T) {
	f, store, _ := incompleteCheckpointCleanupFixture(t, "adopted")
	var id, targetID string
	for _, r := range f.lease.Operation.Resources {
		if r.Kind == "object_storage" {
			id = r.SourceID
			targetID = r.TargetID
			break
		}
	}
	for _, statement := range []string{
		"update object_buckets set state='deleted' where id=$1",
		"update object_buckets set region='foreign' where id=$1",
		"update object_buckets set public_read=true where id=$1",
	} {
		_, err := f.pool.Exec(t.Context(), statement, targetID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_configuration_write_fenced" {
			t.Fatal("unproved target mutation bypassed source hold", err)
		}
	}
	if err := store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, id); err != nil {
		t.Fatal("journal-backed compensation blocked", err)
	}
	_, err := f.pool.Exec(t.Context(), "update object_buckets set state='provisioning' where id=$1", targetID)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_configuration_write_fenced" {
		t.Fatal("completion journal granted revival", err)
	}
}

func TestPGCloneCheckpointIncompleteCleanupGuardMigration(t *testing.T) {
	f, store, _ := incompleteCheckpointCleanupFixture(t, "adopted")
	raw, err := migrations.FS.ReadFile("20261011232006374_clone_checkpoint_unused_reservation_guard.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal("empty cleanup guard round trip", err)
		}
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	var id string
	for _, r := range f.lease.Operation.Resources {
		if r.Kind == "object_storage" {
			id = r.SourceID
			break
		}
	}
	if err = store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, id); err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_unused_guard_down_no_ownership" {
		t.Fatal("owned cleanup guard downgrade", err)
	}
}
