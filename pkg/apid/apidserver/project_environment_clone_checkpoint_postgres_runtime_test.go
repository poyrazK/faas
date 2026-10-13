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
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointRuntimeFailureStore struct {
	*checkpointVerificationFailureStore
	loseRuntimeClaim, loseRuntimeFinish bool
}

func (s *checkpointRuntimeFailureStore) ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, v state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, c state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresRuntime, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresRuntime(ctx, l, id, v, c, r)
	if err == nil && s.loseRuntimeClaim {
		s.loseRuntimeClaim = false
		return state.ProjectEnvironmentCloneCheckpointPostgresRuntime{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointRuntimeFailureStore) FinishProjectEnvironmentCloneCheckpointPostgresRuntime(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation, v state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, c state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneDatabaseTarget, error) {
	target, err := s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresRuntime(ctx, l, id, o, v, c, r)
	if err == nil && s.loseRuntimeFinish {
		s.loseRuntimeFinish = false
		return state.ProjectEnvironmentCloneDatabaseTarget{}, objectstorage.ErrUnavailable
	}
	return target, err
}

type checkpointRuntimeTestDriver struct {
	*checkpointVerificationTestDriver
	qualifies           int
	loseRuntime         bool
	runtime             state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation
	editRuntime         func(*state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation)
	beforeRuntimeFinish func()
}

func (d *checkpointRuntimeTestDriver) QualifyCheckpointPostgresRuntime(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresRuntimePlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_postgres_runtime where operation_id=$1 and source_database_id=$2", p.Verification.Isolation.OperationID, p.Verification.Isolation.SourceDatabaseID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("runtime probe before durable claim", status, err)
	}
	d.qualifies++
	d.runtime = state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation{RequestID: state.ProjectEnvironmentCloneCheckpointPostgresRuntimeRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresRuntimePlanSHA256(p), OwnerID: p.Verification.Isolation.OwnerID, NativeResourceID: p.Verification.Isolation.NativeResourceID, DataResourceID: "private-dataset-" + p.Verification.Isolation.OwnerID, Spec: p.Spec, RuntimeReady: true, DataAccessible: true, PrivateScope: true, ReadOnly: true, ReadyAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseRuntime {
		d.loseRuntime = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointRuntimeTestDriver) ObserveCheckpointPostgresRuntime(_ context.Context, _ state.ProjectEnvironmentCloneCheckpointPostgresRuntimePlan, until time.Time) (state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation, error) {
	if d.runtime.RequestID == "" {
		return d.runtime, objectstorage.ErrNotFound
	}
	out := d.runtime
	out.ReadyThrough = until.Add(time.Hour)
	if d.editRuntime != nil {
		d.editRuntime(&out)
	}
	if d.beforeRuntimeFinish != nil {
		d.beforeRuntimeFinish()
	}
	return out, nil
}
func checkpointRuntimeFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointRuntimeFailureStore, *checkpointRuntimeTestDriver, string) {
	t.Helper()
	f, base, verification, id := checkpointVerificationFixture(t)
	var err error
	f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, verification)
	if err != nil {
		t.Fatal(err)
	}
	store := &checkpointRuntimeFailureStore{checkpointVerificationFailureStore: base}
	f.srv.store = statetest.NewPg(store)
	return f, store, &checkpointRuntimeTestDriver{checkpointVerificationTestDriver: verification}, id
}
func assertCheckpointRuntimePrivate(t *testing.T, f *cloneCoordinatorFixture, s *checkpointRuntimeFailureStore, id string, ready bool) {
	t.Helper()
	fork, err := s.PgStore.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = databases.GetCustomerDatabase(t.Context(), f.lease.Operation.AccountID, fork.AdoptedDatabaseID); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatal("private ready fork exposed", err)
	}
	actual, err := databases.Get(t.Context(), f.lease.Operation.AccountID, fork.AdoptedDatabaseID)
	wantState := managedpostgres.StateProvisioning
	wantGeneration := int64(0)
	if ready {
		wantState = managedpostgres.StateReady
		wantGeneration = 1
	}
	if err != nil || actual.State != wantState || actual.ObservedGeneration != wantGeneration || actual.DesiredGeneration != 1 || (actual.DataResourceID != "") != ready || actual.ProviderResourceID != fork.TargetProviderResourceID {
		t.Fatal("runtime catalogue mismatch", actual, err)
	}
	var role string
	if err := f.pool.QueryRow(t.Context(), "select clone_resource_role from managed_postgres_databases where id=$1", actual.ID).Scan(&role); err != nil || role != "checkpoint" {
		t.Fatal("runtime role exposed", role, err)
	}
	due, err := databases.Due(t.Context(), true, 100, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range due {
		if item.ID == actual.ID {
			t.Fatal("private fork entered general provisioning worker")
		}
	}
}
func TestPGCloneCheckpointPostgresRuntimeLostReplies(t *testing.T) {
	for _, kind := range []string{"native", "claim", "commit"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, id := checkpointRuntimeFixture(t)
			switch kind {
			case "native":
				d.loseRuntime = true
			case "claim":
				s.loseRuntimeClaim = true
			case "commit":
				s.loseRuntimeFinish = true
			}
			var err error
			f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
			if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("expected lost reply", err)
			}
			assertCheckpointRuntimePrivate(t, f, s, id, kind == "commit")
			reclaimCommonCheckpointWorker(t, f)
			before := f.lease.Operation
			if _, err = f.pool.Exec(t.Context(), "update managed_postgres_databases set state='provisioning' where id=$1", id); err != nil {
				t.Fatal(err)
			}
			f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
			if kind == "claim" {
				if !errors.Is(err, objectstorage.ErrNotFound) || d.qualifies != 0 {
					t.Fatal("uncertain runtime request redispatched", err)
				}
				return
			}
			if err != nil || d.qualifies != 1 || !reflect.DeepEqual(before, f.lease.Operation) {
				t.Fatal("runtime recovery changed original checkpoint", err)
			}
			assertCheckpointRuntimePrivate(t, f, s, id, true)
			if _, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d); err != nil {
				t.Fatal("ready fork isolation replay", err)
			}
			if _, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d); err != nil {
				t.Fatal("ready fork verification replay", err)
			}
			if _, complete, err := f.srv.restoreCheckpointPostgres(t.Context(), f.lease, d, 10); err != nil || !complete {
				t.Fatal("ready fork restore replay", err)
			}
			f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
			if err != nil || d.qualifies != 1 || d.verifies != 1 || d.isolates != 1 || d.creates != 1 {
				t.Fatal("ready replay repeated native IO", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresRuntimeRejectsProof(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation){
		"owner": func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.OwnerID = "foreign" },
		"resource": func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) {
			o.NativeResourceID = "source"
		},
		"request": func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) {
			o.RequestID = "replacement"
		},
		"plan": func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) {
			o.PlanSHA256 = strings.Repeat("d", 64)
		},
		"dataset": func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.DataResourceID = "" },
		"dataset-control": func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) {
			o.DataResourceID = "invalid\n"
		},
		"spec":     func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.Spec.PostgresMajor++ },
		"health":   func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.RuntimeReady = false },
		"access":   func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.DataAccessible = false },
		"private":  func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.PrivateScope = false },
		"readonly": func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.ReadOnly = false },
		"horizon": func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) {
			o.ReadyThrough = time.Now().Add(time.Minute)
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f, s, d, id := checkpointRuntimeFixture(t)
			d.editRuntime = edit
			var err error
			f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("incomplete runtime proof accepted", err)
			}
			assertCheckpointRuntimePrivate(t, f, s, id, false)
			d.editRuntime = nil
			f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
			if err != nil || d.qualifies != 1 {
				t.Fatal("runtime proof recovery redispatched", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresRuntimeRechecksPrerequisites(t *testing.T) {
	for _, kind := range []string{"closure", "retention", "verification", "worker"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, id := checkpointRuntimeFixture(t)
			d.beforeRuntimeFinish = func() {
				d.beforeRuntimeFinish = nil
				switch kind {
				case "closure":
					d.editIsolation = func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
						o.AllWritersClosed = false
					}
				case "retention":
					d.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
						o.RetainedUntil = time.Now().Add(time.Minute)
					}
				case "verification":
					d.editVerification = func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
						o.SourceSHA256 = strings.Repeat("d", 64)
						o.TargetSHA256 = o.SourceSHA256
					}
				case "worker":
					reclaimCommonCheckpointWorker(t, f)
				}
			}
			if _, err := f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("lost prerequisite accepted", err)
			}
			assertCheckpointRuntimePrivate(t, f, s, id, false)
			d.editIsolation = nil
			d.editRelease = nil
			d.editVerification = nil
			var err error
			f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
			if err != nil || d.qualifies != 1 {
				t.Fatal("prerequisite recovery repeated qualification", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresRuntimeLateFailureAndMigration(t *testing.T) {
	f, s, d, id := checkpointRuntimeFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006327_clone_checkpoint_postgres_runtime.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	publication, err := migrations.FS.ReadFile("20261011232006343_clone_checkpoint_pg_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(publication), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	activation, err := migrations.FS.ReadFile("20261011232006339_clone_checkpoint_pg_activations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(activation), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	connection, err := migrations.FS.ReadFile("20261011232006336_clone_checkpoint_pg_connection_values.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(connection), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	retirement, err := migrations.FS.ReadFile("20261011232006333_clone_checkpoint_postgres_credential_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(retirement), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	credential, err := migrations.FS.ReadFile("20261011232006330_clone_checkpoint_postgres_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(credential), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), `create function reject_private_runtime_ready() returns trigger language plpgsql as $$ begin if NEW.state='ready' and NEW.clone_resource_role='checkpoint' then raise exception 'injected final runtime catalogue failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_private_runtime_ready before update on managed_postgres_databases for each row execute function reject_private_runtime_ready()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d); err == nil {
		t.Fatal("expected late catalogue failure")
	}
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresRuntime(t.Context(), f.lease, id)
	if err != nil || in.State != "requested" || in.ObservationSHA256 != "" {
		t.Fatal("runtime receipt escaped rollback", in, err)
	}
	assertCheckpointRuntimePrivate(t, f, s, id, false)
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "clone_checkpoint_postgres_runtime_down_no_ownership" {
		t.Fatal("downgrade erased runtime authority", err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger reject_private_runtime_ready on managed_postgres_databases; drop function reject_private_runtime_ready()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
	if err != nil || d.qualifies != 1 {
		t.Fatal("late failure recovery repeated runtime probe", err)
	}
	assertCheckpointRuntimePrivate(t, f, s, id, true)
}
func TestPGCloneCheckpointPostgresRuntimeCatalogueSubstitution(t *testing.T) {
	f, s, d, id := checkpointRuntimeFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresRuntime(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	owner := in.Plan.Verification.Isolation.OwnerID
	for _, sql := range []string{
		"update managed_postgres_databases set data_resource_id='foreign' where id=$1",
		"update managed_postgres_databases set desired_generation=2,observed_generation=2 where id=$1",
		"update managed_postgres_databases set clone_resource_role='target' where id=$1",
	} {
		// Commit each substitution so the independent lease reader sees it.
		if _, err = f.pool.Exec(t.Context(), sql, owner); err != nil {
			t.Fatal(err)
		}
		if _, err = s.PgStore.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, id); !errors.Is(err, state.ErrConflict) {
			t.Fatal("substituted ready catalogue authenticated", err)
		}
		if _, err = f.pool.Exec(t.Context(), "update managed_postgres_databases set data_resource_id=$2,desired_generation=1,observed_generation=1,clone_resource_role='checkpoint' where id=$1", owner, in.Observation.DataResourceID); err != nil {
			t.Fatal(err)
		}
	}
	d.editRuntime = func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) {
		o.DataResourceID = "replacement"
	}
	if _, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("replacement runtime dataset accepted", err)
	}
	assertCheckpointRuntimePrivate(t, f, s, id, true)
}
func TestPGCloneCheckpointPostgresRuntimeRequiresVerification(t *testing.T) {
	f, s, _, id := checkpointVerificationFixture(t)
	if _, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresRuntime(t.Context(), f.lease, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unverified fork qualified", err)
	}
}
func TestPGCloneCheckpointPostgresRuntimeReadyCompensation(t *testing.T) {
	f, s, d, id := checkpointRuntimeFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	op := f.lease.Operation
	f.lease.Operation, err = s.PgStore.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	// Transitioning operation status does not transfer worker authority.
	reclaimCommonCheckpointWorker(t, f)
	cleanup := &checkpointPgTargetCompensationTestDriver{t: t, f: f}
	f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, cleanup)
	if err != nil {
		t.Fatal("ready fork compensation failed", err)
	}
	fork, err := s.PgStore.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, id)
	if err != nil || fork.State != "deleted" {
		t.Fatal("ready fork not retired", err)
	}
	f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, cleanup)
	if err != nil || cleanup.calls != 1 {
		t.Fatal("ready fork retirement replay", err)
	}
	var status string
	if err = f.pool.QueryRow(t.Context(), "select state from managed_postgres_databases where id=$1", fork.AdoptedDatabaseID).Scan(&status); err != nil || status != "deleted" {
		t.Fatal("catalogue not retired", status, err)
	}
}

func TestPGCloneCheckpointPostgresRuntimeRejectsSourceDataset(t *testing.T) {
	f, s, d, id := checkpointRuntimeFixture(t)
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresRuntime(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	d.editRuntime = func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) {
		o.DataResourceID = in.Plan.Verification.Isolation.SourceDataResourceID
	}
	if _, err = f.srv.prepareCheckpointPostgresRuntime(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("source dataset prepared for stage", err)
	}
	assertCheckpointRuntimePrivate(t, f, s, id, false)
}
