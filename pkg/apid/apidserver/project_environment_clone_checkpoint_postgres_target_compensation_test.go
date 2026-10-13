//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointPgTargetCompensationFailureStore struct {
	PgStore                            *state.PgStore
	loseReserve, loseClaim, loseFinish bool
}

func (s *checkpointPgTargetCompensationFailureStore) ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, l, id)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return in, objectstorage.ErrUnavailable
	}
	return in, err
}
func (s *checkpointPgTargetCompensationFailureStore) ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, l, id)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return in, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointPgTargetCompensationFailureStore) FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, l, id, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointPgTargetCompensationTestDriver struct {
	t                      *testing.T
	f                      *cloneCoordinatorFixture
	calls, reads           int
	loseNative, mutatePlan bool
	proof                  state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation
	edit                   func(*state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation)
	beforeFinish           func()
}

func checkpointPgTargetCompensationProof(p state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation {
	o := state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation{RequestID: state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlanSHA256(p), OwnerID: p.Restore.TargetOwnerID, RestoreRequestID: p.Restore.TargetOwnerID, TargetProviderResourceID: p.Restore.TargetProviderResourceID, RestorePermanentlyFenced: true, TargetAbsent: true, SourceUnchanged: true, SnapshotUnchanged: true, CompensatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if p.Isolation != nil {
		o.IsolationRequestID = state.ProjectEnvironmentCloneCheckpointPostgresIsolationRequestID(p.Isolation.Plan)
		o.IsolationPlanSHA256 = state.ProjectEnvironmentCloneCheckpointPostgresIsolationPlanSHA256(p.Isolation.Plan)
		o.IsolationPermanentlyFenced = true
	}
	return o
}
func (d *checkpointPgTargetCompensationTestDriver) CompensateCheckpointPostgresTarget(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_pg_target_compensations where operation_id=$1 and source_database_id=$2", p.Restore.OperationID, p.Restore.SourceDatabaseID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("native cleanup before durable claim", status, err)
	}
	d.calls++
	d.proof = checkpointPgTargetCompensationProof(p)
	if d.mutatePlan && p.Isolation != nil {
		p.Isolation.Plan.NativeResourceID = "driver mutation"
	}
	if d.loseNative {
		d.loseNative = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointPgTargetCompensationTestDriver) ObserveCheckpointPostgresTargetCompensation(_ context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) (state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation, error) {
	d.reads++
	if d.proof.RequestID == "" {
		return d.proof, objectstorage.ErrNotFound
	}
	if p.Isolation != nil && p.Isolation.Plan.NativeResourceID == "driver mutation" {
		d.t.Fatal("driver changed saved plan")
	}
	o := d.proof
	if d.edit != nil {
		d.edit(&o)
	}
	if d.beforeFinish != nil {
		d.beforeFinish()
	}
	return o, nil
}
func checkpointPgTargetCompensationFixture(t *testing.T, phase string) (*cloneCoordinatorFixture, *checkpointPgTargetCompensationFailureStore, *checkpointPgTargetCompensationTestDriver, string) {
	t.Helper()
	f, base, driver, id := checkpointPostgresRestoreFixture(t)
	var err error
	switch phase {
	case "absent":
	case "reserved":
		_, err = base.PgStore.ReserveProjectEnvironmentClonePostgresSnapshotRestore(t.Context(), f.lease, id, 10)
	case "unknown":
		driver.loseCreate = true
		f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
		if errors.Is(err, objectstorage.ErrUnavailable) {
			err = nil
		}
	default:
		f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
		if err == nil && phase != "restored" {
			iso := &checkpointIsolationTestDriver{checkpointPostgresRestoreTestDriver: driver, f: f}
			if phase == "isolation-planned" {
				_, err = base.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresIsolation(t.Context(), f.lease, id)
			} else {
				if phase == "isolation-unknown" {
					iso.loseIsolation = true
				}
				f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, iso)
				if phase == "isolation-unknown" && errors.Is(err, objectstorage.ErrUnavailable) {
					err = nil
				}
			}
		}
	}
	if err != nil {
		t.Fatal("target setup", err)
	}
	op := f.lease.Operation
	f.lease.Operation, err = base.PgStore.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	store := &checkpointPgTargetCompensationFailureStore{PgStore: base.PgStore}
	f.srv.store = statetest.NewPg(store)
	return f, store, &checkpointPgTargetCompensationTestDriver{t: t, f: f}, id
}
func assertCheckpointPgTargetCompensation(t *testing.T, f *cloneCoordinatorFixture, s *checkpointPgTargetCompensationFailureStore, id, want string) state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensation {
	t.Helper()
	in, err := s.PgStore.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationForLease(t.Context(), f.lease, id)
	if err != nil || in.State != want {
		t.Fatal("compensation journal", in.State, err)
	}
	if want == "compensated" {
		fork, err := s.PgStore.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, id)
		if err != nil || fork.State != "deleted" {
			t.Fatal("fork not retired", fork.State, err)
		}
		if fork.AdoptedDatabaseID != "" {
			var status string
			if err = f.pool.QueryRow(t.Context(), "select state from managed_postgres_databases where id=$1", fork.AdoptedDatabaseID).Scan(&status); err != nil || status != "deleted" {
				t.Fatal("catalogue not retired", status, err)
			}
		}
	}
	return in
}
func TestPGCloneCheckpointPgTargetCompensationOriginalStates(t *testing.T) {
	for _, phase := range []string{"absent", "reserved", "unknown", "restored", "isolation-planned", "isolation-unknown", "adopted"} {
		t.Run(phase, func(t *testing.T) {
			f, s, d, id := checkpointPgTargetCompensationFixture(t, phase)
			d.mutatePlan = true
			var err error
			f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "absent" {
				if d.calls != 0 || d.reads != 0 {
					t.Fatal("absent intent dispatched native cleanup")
				}
				return
			}
			in := assertCheckpointPgTargetCompensation(t, f, s, id, "compensated")
			if d.calls != 1 {
				t.Fatal("native dispatch count", d.calls)
			}
			if phase == "adopted" {
				if _, err = s.PgStore.BeginProjectEnvironmentClonePostgresSnapshotCleanup(t.Context(), f.lease, id); err != nil {
					t.Fatal("snapshot cleanup after target retirement", err)
				}
				if _, err = s.PgStore.FinishProjectEnvironmentClonePostgresSnapshotCleanup(t.Context(), f.lease, id); err != nil {
					t.Fatal(err)
				}
			}
			before := d.reads
			reclaimCommonCheckpointWorker(t, f)
			f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, d)
			if err != nil || d.calls != 1 || d.reads != before {
				t.Fatal("completed recovery repeated native IO", err)
			}
			changed := in.Observation
			changed.CompensatedAt = changed.CompensatedAt.Add(time.Microsecond)
			if err = s.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(t.Context(), f.lease, id, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed terminal proof accepted", err)
			}
		})
	}
}
func TestPGCloneCheckpointPgTargetCompensationLostReplies(t *testing.T) {
	for _, phase := range []string{"reserve", "claim", "native", "finish"} {
		t.Run(phase, func(t *testing.T) {
			f, s, d, id := checkpointPgTargetCompensationFixture(t, "adopted")
			switch phase {
			case "reserve":
				s.loseReserve = true
			case "claim":
				s.loseClaim = true
			case "native":
				d.loseNative = true
			case "finish":
				s.loseFinish = true
			}
			var err error
			f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, d)
			if err == nil {
				t.Fatal("lost reply ignored")
			}
			old := f.lease
			reclaimCommonCheckpointWorker(t, f)
			if _, _, err = s.ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(t.Context(), old, id); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale worker accepted", err)
			}
			if phase == "claim" {
				in := assertCheckpointPgTargetCompensation(t, f, s, id, "requested")
				d.proof = checkpointPgTargetCompensationProof(in.Plan)
			}
			f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, d)
			expected := 1
			if phase == "claim" {
				expected = 0
			}
			if err != nil || d.calls != expected {
				t.Fatal("recovery redispatched cleanup", d.calls, err)
			}
			assertCheckpointPgTargetCompensation(t, f, s, id, "compensated")
		})
	}
}
func TestPGCloneCheckpointPgTargetCompensationProofAndLegacyGuards(t *testing.T) {
	f, s, d, id := checkpointPgTargetCompensationFixture(t, "adopted")
	in, err := s.ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	in, _, err = s.ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.CompensateCheckpointPostgresTarget(t.Context(), in.Plan); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation){
		func(o *state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.RestorePermanentlyFenced = false
		},
		func(o *state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.IsolationPermanentlyFenced = false
		},
		func(o *state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.TargetAbsent = false
		},
		func(o *state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.SourceUnchanged = false
		},
		func(o *state.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) {
			o.TargetProviderResourceID = "foreign-target"
		},
	} {
		o := d.proof
		edit(&o)
		if err = s.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(t.Context(), f.lease, id, o); !errors.Is(err, state.ErrConflict) {
			t.Fatal("invalid proof accepted", err)
		}
	}
	if _, err = s.PgStore.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("legacy abandonment accepted", err)
	}
	if _, err = s.PgStore.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(t.Context(), f.lease, id, state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{Done: true}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("legacy deletion accepted", err)
	}
	assertCheckpointPgTargetCompensation(t, f, s, id, "requested")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = s.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, f.lease, id, d.proof); err == nil {
		t.Fatal("cancelled finish succeeded")
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(t.Context(), f.lease, id, d.proof); err != nil {
		t.Fatal(err)
	}
	assertCheckpointPgTargetCompensation(t, f, s, id, "compensated")
}
func TestPGCloneCheckpointPgTargetCompensationAtomicAndMigration(t *testing.T) {
	f, s, d, id := checkpointPgTargetCompensationFixture(t, "adopted")
	raw, err := migrations.FS.ReadFile("20261011232006380_clone_checkpoint_pg_target_compensation.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), q); err != nil {
			t.Fatal("empty migration round trip", err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), `create function reject_pg_target_finish() returns trigger language plpgsql as $$ begin if NEW.state='deleted' then raise exception 'injected final target failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_pg_target_finish before update on project_environment_clone_postgres_snapshot_restores for each row execute function reject_pg_target_finish()`); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, d)
	if err == nil {
		t.Fatal("final failure ignored")
	}
	in := assertCheckpointPgTargetCompensation(t, f, s, id, "requested")
	var status string
	if err = f.pool.QueryRow(t.Context(), "select state from managed_postgres_databases where id=$1", in.Plan.Restore.TargetOwnerID).Scan(&status); err != nil || status != "provisioning" {
		t.Fatal("catalogue retirement did not roll back", status, err)
	}
	assertDown := func() {
		t.Helper()
		tx, err := f.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), parts[1])
		_ = tx.Rollback(t.Context())
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_pg_target_compensations_down_no_ownership" {
			t.Fatal("downgrade erased compensation authority", err)
		}
	}
	assertDown()
	if _, err = f.pool.Exec(t.Context(), "drop trigger reject_pg_target_finish on project_environment_clone_postgres_snapshot_restores; drop function reject_pg_target_finish()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, d)
	if err != nil || d.calls != 1 {
		t.Fatal("rollback redispatched native cleanup", err)
	}
	done := assertCheckpointPgTargetCompensation(t, f, s, id, "compensated")
	if !reflect.DeepEqual(done.Plan, in.Plan) {
		t.Fatal("recovery changed original plan")
	}
	assertDown()
}
func TestPGCloneCheckpointPgTargetCompensationCredentialGate(t *testing.T) {
	f, credentials, driver, _, id := checkpointCredentialRetirementFixture(t)
	cleanup := &checkpointPgTargetCompensationTestDriver{t: t, f: f}
	if _, err := f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, cleanup); !errors.Is(err, state.ErrConflict) || cleanup.calls != 0 {
		t.Fatal("unretired credentials allowed native deletion", err)
	}
	var err error
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, driver)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, cleanup)
	if err != nil {
		t.Fatal("retired credentials blocked cleanup", err)
	}
	s := &checkpointPgTargetCompensationFailureStore{PgStore: credentials.PgStore}
	assertCheckpointPgTargetCompensation(t, f, s, id, "compensated")
}

func TestPGCloneCheckpointPgTargetCompensationRejectsChangedOwners(t *testing.T) {
	for _, kind := range []string{"snapshot", "catalogue", "missing-isolation"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, id := checkpointPgTargetCompensationFixture(t, "adopted")
			in, err := s.ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(t.Context(), f.lease, id)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "snapshot" {
				_, err = f.pool.Exec(t.Context(), "update project_environment_clone_postgres_snapshots set provider_snapshot_id='foreign-snapshot' where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id)
			} else if kind == "missing-isolation" {
				_, err = f.pool.Exec(t.Context(), "delete from project_environment_clone_checkpoint_postgres_isolation where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id)
			} else {
				foreignFingerprint := strings.Repeat("a", 64)
				if foreignFingerprint == in.Plan.Restore.BackendFingerprint {
					foreignFingerprint = strings.Repeat("b", 64)
				}
				_, err = f.pool.Exec(t.Context(), "update managed_postgres_databases set backend_fingerprint=$2 where id=$1", in.Plan.Restore.TargetOwnerID, foreignFingerprint)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) || d.calls != 0 {
				t.Fatal("changed ownership allowed native cleanup", err)
			}
		})
	}
}
