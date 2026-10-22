//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type checkpointIsolationFailureStore struct {
	*checkpointPostgresRestoreFailureStore
	loseIsolationClaim, loseIsolationFinish bool
}

func (s *checkpointIsolationFailureStore) ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresIsolation, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresIsolation(ctx, l, id, r)
	if err == nil && s.loseIsolationClaim {
		s.loseIsolationClaim = false
		return state.ProjectEnvironmentCloneCheckpointPostgresIsolation{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointIsolationFailureStore) FinishProjectEnvironmentCloneCheckpointPostgresIsolation(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneDatabaseTarget, error) {
	target, err := s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresIsolation(ctx, l, id, o, r)
	if err == nil && s.loseIsolationFinish {
		s.loseIsolationFinish = false
		return state.ProjectEnvironmentCloneDatabaseTarget{}, objectstorage.ErrUnavailable
	}
	return target, err
}

type checkpointIsolationTestDriver struct {
	*checkpointPostgresRestoreTestDriver
	f                     *cloneCoordinatorFixture
	isolates              int
	loseIsolation         bool
	terminal              state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation
	editIsolation         func(*state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation)
	beforeIsolationFinish func()
}

func (d *checkpointIsolationTestDriver) IsolateCheckpointPostgres(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresIsolationPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_postgres_isolation where operation_id=$1 and source_database_id=$2", p.OperationID, p.SourceDatabaseID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("SQL isolation before durable claim", status, err)
	}
	d.isolates++
	d.terminal = state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation{RequestID: state.ProjectEnvironmentCloneCheckpointPostgresIsolationRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresIsolationPlanSHA256(p), OwnerID: p.OwnerID, NativeResourceID: p.NativeResourceID, RosterSHA256: strings.Repeat("a", 64), AllWritersClosed: true, InheritedLoginsDisabled: true, ScheduledJobsDisabled: true, PrivateScope: true, ClosedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseIsolation {
		d.loseIsolation = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointIsolationTestDriver) ObserveCheckpointPostgresIsolation(_ context.Context, _ state.ProjectEnvironmentCloneCheckpointPostgresIsolationPlan, until time.Time) (state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, error) {
	if d.terminal.RequestID == "" {
		return d.terminal, objectstorage.ErrNotFound
	}
	out := d.terminal
	out.ClosedThrough = until.Add(time.Hour)
	if d.editIsolation != nil {
		d.editIsolation(&out)
	}
	if d.beforeIsolationFinish != nil {
		d.beforeIsolationFinish()
	}
	return out, nil
}
func checkpointIsolationFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointIsolationFailureStore, *checkpointIsolationTestDriver, string) {
	t.Helper()
	f, base, restore, id := checkpointPostgresRestoreFixture(t)
	store := &checkpointIsolationFailureStore{checkpointPostgresRestoreFailureStore: base}
	f.srv.store = statetest.NewPg(store)
	driver := &checkpointIsolationTestDriver{checkpointPostgresRestoreTestDriver: restore, f: f}
	var err error
	f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if err != nil {
		t.Fatal(err)
	}
	return f, store, driver, id
}
func assertCheckpointIsolationPrivate(t *testing.T, f *cloneCoordinatorFixture, store *checkpointIsolationFailureStore, id string, adopted bool) {
	t.Helper()
	fork, err := store.PgStore.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	if (fork.State == "adopted") != adopted {
		t.Fatal("fork adoption state", fork.State)
	}
	if !adopted {
		return
	}
	databases, err := managedpostgres.NewPostgresStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := databases.GetCustomerDatabase(t.Context(), f.lease.Operation.AccountID, fork.AdoptedDatabaseID); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatal("private SQL fork exposed", err)
	}
	var role, status string
	if err := f.pool.QueryRow(t.Context(), "select clone_resource_role,state from managed_postgres_databases where id=$1", fork.AdoptedDatabaseID).Scan(&role, &status); err != nil || role != "checkpoint" || status != "provisioning" {
		t.Fatal("fork ready or public", role, status, err)
	}
}
func TestPGCloneCheckpointPostgresIsolationLostNativeReply(t *testing.T) {
	f, store, d, id := checkpointIsolationFixture(t)
	d.loseIsolation = true
	var err error
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrUnavailable) || d.isolates != 1 {
		t.Fatal("lost SQL reply", err)
	}
	assertCheckpointIsolationPrivate(t, f, store, id, false)
	if _, err := f.pool.Exec(t.Context(), "update managed_postgres_databases set state='provisioning' where id=$1", id); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	before := f.lease.Operation
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if err != nil || d.isolates != 1 || !reflect.DeepEqual(before, f.lease.Operation) {
		t.Fatal("isolation recovery changed original inventory", err)
	}
	assertCheckpointIsolationPrivate(t, f, store, id, true)
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if err != nil || d.isolates != 1 || d.creates != 1 {
		t.Fatal("isolation or restore repeated", err)
	}
	if _, complete, err := f.srv.restoreCheckpointPostgres(t.Context(), f.lease, d, 10); err != nil || !complete || d.creates != 1 {
		t.Fatal("adopted fork restore replay", err)
	}
}
func TestPGCloneCheckpointPostgresIsolationLostClaim(t *testing.T) {
	f, store, d, id := checkpointIsolationFixture(t)
	store.loseIsolationClaim = true
	var err error
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrUnavailable) || d.isolates != 0 {
		t.Fatal("unknown claim dispatched SQL", err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrNotFound) || d.isolates != 0 {
		t.Fatal("unknown SQL request redispatched", err)
	}
	assertCheckpointIsolationPrivate(t, f, store, id, false)
	raw, err := migrations.FS.ReadFile("20261011232006322_clone_checkpoint_postgres_isolation.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), strings.SplitN(string(raw), "-- +goose Down", 2)[1])
	_ = tx.Rollback(t.Context())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "clone_checkpoint_postgres_isolation_down_no_ownership" {
		t.Fatal("downgrade lost isolation authority", err)
	}
}
func TestPGCloneCheckpointPostgresIsolationLostCommit(t *testing.T) {
	f, store, d, id := checkpointIsolationFixture(t)
	store.loseIsolationFinish = true
	var err error
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if err != nil || d.isolates != 1 {
		t.Fatal("lost adoption reply repeated SQL", err)
	}
	assertCheckpointIsolationPrivate(t, f, store, id, true)
}
func TestPGCloneCheckpointPostgresIsolationRejectsIncompleteProof(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation){
		"owner": func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) { o.OwnerID = "foreign" },
		"resource": func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
			o.NativeResourceID = "source"
		},
		"request": func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
			o.RequestID = "replacement"
		},
		"writers": func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
			o.AllWritersClosed = false
		},
		"logins": func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
			o.InheritedLoginsDisabled = false
		},
		"jobs": func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
			o.ScheduledJobsDisabled = false
		},
		"scope": func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) { o.PrivateScope = false },
		"horizon": func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
			o.ClosedThrough = time.Now().Add(time.Minute)
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f, store, d, id := checkpointIsolationFixture(t)
			d.editIsolation = edit
			var err error
			f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("incomplete isolation adopted", err)
			}
			assertCheckpointIsolationPrivate(t, f, store, id, false)
			d.editIsolation = nil
			f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
			if err != nil || d.isolates != 1 {
				t.Fatal("valid proof recovery repeated SQL", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresIsolationLateFailureRollsBack(t *testing.T) {
	f, store, d, id := checkpointIsolationFixture(t)
	if _, err := f.pool.Exec(t.Context(), `create function reject_isolation_adoption() returns trigger language plpgsql as $$ begin if NEW.state='adopted' then raise exception 'injected late isolation failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_isolation_adoption before update on project_environment_clone_checkpoint_postgres_isolation for each row execute function reject_isolation_adoption()`); err != nil {
		t.Fatal(err)
	}
	var err error
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if err == nil {
		t.Fatal("expected late failure")
	}
	assertCheckpointIsolationPrivate(t, f, store, id, false)
	var count int
	if err := f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_databases where environment_clone_operation_id=$1", f.lease.Operation.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("catalogue escaped rollback", count, err)
	}
	if _, err := f.pool.Exec(t.Context(), "drop trigger reject_isolation_adoption on project_environment_clone_checkpoint_postgres_isolation; drop function reject_isolation_adoption()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if err != nil || d.isolates != 1 {
		t.Fatal("rollback recovery repeated SQL", err)
	}
	assertCheckpointIsolationPrivate(t, f, store, id, true)
}
func TestPGCloneCheckpointPostgresIsolationStaleWorkerAndRetention(t *testing.T) {
	f, store, d, id := checkpointIsolationFixture(t)
	old := f.lease
	d.beforeIsolationFinish = func() { d.beforeIsolationFinish = nil; reclaimCommonCheckpointWorker(t, f) }
	if _, err := f.srv.isolateCheckpointPostgres(t.Context(), old, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker adopted", err)
	}
	d.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
		o.RetainedUntil = time.Now().Add(time.Minute)
	}
	if _, err := f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired captures adopted", err)
	}
	assertCheckpointIsolationPrivate(t, f, store, id, false)
	d.editRelease = nil
	var err error
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if err != nil || d.isolates != 1 {
		t.Fatal("handoff repeated SQL", err)
	}
}

func TestPGCloneCheckpointPostgresIsolationReopenedAndChangedRoster(t *testing.T) {
	f, store, d, id := checkpointIsolationFixture(t)
	var err error
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation){
		func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
			o.AllWritersClosed = false
		},
		func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
			o.RosterSHA256 = strings.Repeat("b", 64)
		},
	} {
		d.editIsolation = edit
		if _, err := f.srv.isolateCheckpointPostgres(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
			t.Fatal("changed isolation adopted", err)
		}
		assertCheckpointIsolationPrivate(t, f, store, id, true)
	}
	if d.isolates != 1 {
		t.Fatal("replay repaired SQL closure")
	}
}
func TestPGCloneCheckpointPostgresIsolationMigrationRoundTrip(t *testing.T) {
	f, _, _, _ := checkpointIsolationFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006322_clone_checkpoint_postgres_isolation.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
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
	runtime, err := migrations.FS.ReadFile("20261011232006327_clone_checkpoint_postgres_runtime.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(runtime), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	// The later verification journal owns a restrictive FK to this table.
	// Retire its empty authority first when exercising the predecessor round trip.
	verification, err := migrations.FS.ReadFile("20261011232006325_clone_checkpoint_postgres_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), strings.SplitN(string(verification), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
}
