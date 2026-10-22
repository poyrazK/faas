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

type checkpointVerificationFailureStore struct {
	*checkpointIsolationFailureStore
	loseClaim, loseFinish bool
}

func (s *checkpointVerificationFailureStore) ClaimProjectEnvironmentCloneCheckpointPostgresVerification(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, c state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointPostgresVerification, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointPostgresVerification(ctx, l, id, c, r)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return state.ProjectEnvironmentCloneCheckpointPostgresVerification{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointVerificationFailureStore) FinishProjectEnvironmentCloneCheckpointPostgresVerification(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, c state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointPostgresVerification(ctx, l, id, o, c, r)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointVerificationTestDriver struct {
	*checkpointIsolationTestDriver
	verifies                 int
	loseVerify               bool
	verification             state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation
	editVerification         func(*state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation)
	beforeVerificationFinish func()
}

func (d *checkpointVerificationTestDriver) VerifyCheckpointPostgres(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointPostgresVerificationPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_postgres_verification where operation_id=$1 and source_database_id=$2", p.Isolation.OperationID, p.Isolation.SourceDatabaseID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("verification before durable claim", status, err)
	}
	d.verifies++
	d.verification = state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation{RequestID: state.ProjectEnvironmentCloneCheckpointPostgresVerificationRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresVerificationPlanSHA256(p), SourceSHA256: strings.Repeat("c", 64), TargetSHA256: strings.Repeat("c", 64), Complete: true, UnsupportedObjectsAbsent: true, ReadOnly: true, SourceReadBytes: 100, TargetReadBytes: 100, ComparedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseVerify {
		d.loseVerify = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointVerificationTestDriver) ObserveCheckpointPostgresVerification(_ context.Context, _ state.ProjectEnvironmentCloneCheckpointPostgresVerificationPlan) (state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation, error) {
	if d.verification.RequestID == "" {
		return d.verification, objectstorage.ErrNotFound
	}
	out := d.verification
	if d.editVerification != nil {
		d.editVerification(&out)
	}
	if d.beforeVerificationFinish != nil {
		d.beforeVerificationFinish()
	}
	return out, nil
}
func checkpointVerificationFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointVerificationFailureStore, *checkpointVerificationTestDriver, string) {
	t.Helper()
	f, base, isolation, id := checkpointIsolationFixture(t)
	var err error
	f.lease, err = f.srv.isolateCheckpointPostgres(t.Context(), f.lease, isolation)
	if err != nil {
		t.Fatal(err)
	}
	store := &checkpointVerificationFailureStore{checkpointIsolationFailureStore: base}
	f.srv.store = statetest.NewPg(store)
	return f, store, &checkpointVerificationTestDriver{checkpointIsolationTestDriver: isolation}, id
}
func assertCheckpointVerification(t *testing.T, f *cloneCoordinatorFixture, store *checkpointVerificationFailureStore, id, want string) {
	t.Helper()
	in, err := store.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresVerification(t.Context(), f.lease, id)
	if err != nil || in.State != want {
		t.Fatal("verification state", in.State, err)
	}
	assertCheckpointIsolationPrivate(t, f, store.checkpointIsolationFailureStore, id, true)
}
func TestPGCloneCheckpointPostgresVerificationLostReplies(t *testing.T) {
	for _, kind := range []string{"native", "claim", "commit"} {
		t.Run(kind, func(t *testing.T) {
			f, store, d, id := checkpointVerificationFixture(t)
			switch kind {
			case "native":
				d.loseVerify = true
			case "claim":
				store.loseClaim = true
			case "commit":
				store.loseFinish = true
			}
			var err error
			f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d)
			if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("expected lost reply", err)
			}
			reclaimCommonCheckpointWorker(t, f)
			before := f.lease.Operation
			if _, err := f.pool.Exec(t.Context(), "update managed_postgres_databases set state='provisioning' where id=$1", id); err != nil {
				t.Fatal(err)
			}
			f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d)
			if kind == "claim" {
				if !errors.Is(err, objectstorage.ErrNotFound) || d.verifies != 0 {
					t.Fatal("unknown request dispatched", err)
				}
				assertCheckpointVerification(t, f, store, id, "requested")
				return
			}
			if err != nil || d.verifies != 1 || !reflect.DeepEqual(before, f.lease.Operation) {
				t.Fatal("recovery changed checkpoint or repeated comparison", err)
			}
			assertCheckpointVerification(t, f, store, id, "verified")
			f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d)
			if err != nil || d.verifies != 1 || d.isolates != 1 || d.creates != 1 {
				t.Fatal("replay repeated native IO", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresVerificationRejectsProof(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation){
		"request": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.RequestID = "foreign"
		},
		"plan": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.PlanSHA256 = strings.Repeat("d", 64)
		},
		"mismatch": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.TargetSHA256 = strings.Repeat("d", 64)
		},
		"missing-digest": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.SourceSHA256 = ""
			o.TargetSHA256 = ""
		},
		"incomplete": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) { o.Complete = false },
		"unsupported": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.UnsupportedObjectsAbsent = false
		},
		"mutation": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) { o.ReadOnly = false },
		"negative": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.SourceReadBytes = -1
		},
		"source-cap": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.SourceReadBytes = 1<<40 + 1
		},
		"target-cap": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.TargetReadBytes = 1<<40 + 1
		},
		"time": func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
			o.ComparedAt = time.Time{}
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f, store, d, id := checkpointVerificationFixture(t)
			d.editVerification = edit
			var err error
			f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("bad data proof accepted", err)
			}
			assertCheckpointVerification(t, f, store, id, "requested")
			d.editVerification = nil
			f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d)
			if err != nil || d.verifies != 1 {
				t.Fatal("original proof recovery", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresVerificationRechecksClosureAndRetention(t *testing.T) {
	for _, kind := range []string{"closure", "retention", "worker"} {
		t.Run(kind, func(t *testing.T) {
			f, store, d, id := checkpointVerificationFixture(t)
			d.beforeVerificationFinish = func() {
				d.beforeVerificationFinish = nil
				switch kind {
				case "closure":
					d.editIsolation = func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
						o.AllWritersClosed = false
					}
				case "retention":
					d.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
						o.RetainedUntil = time.Now().Add(time.Minute)
					}
				case "worker":
					reclaimCommonCheckpointWorker(t, f)
				}
			}
			old := f.lease
			if _, err := f.srv.verifyCheckpointPostgres(t.Context(), old, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("lost closure, retention or worker accepted", err)
			}
			assertCheckpointVerification(t, f, store, id, "requested")
			d.editIsolation = nil
			d.editRelease = nil
			var err error
			f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d)
			if err != nil || d.verifies != 1 {
				t.Fatal("handoff repeated data reads", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresVerificationReplayChangedProof(t *testing.T) {
	f, store, d, id := checkpointVerificationFixture(t)
	var err error
	f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	d.editVerification = func(o *state.ProjectEnvironmentCloneCheckpointPostgresVerificationObservation) {
		o.SourceSHA256 = strings.Repeat("d", 64)
		o.TargetSHA256 = o.SourceSHA256
	}
	if _, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("replacement equal proof accepted", err)
	}
	d.editVerification = nil
	d.editIsolation = func(o *state.ProjectEnvironmentCloneCheckpointPostgresIsolationObservation) {
		o.RosterSHA256 = strings.Repeat("d", 64)
	}
	if _, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed closed roster accepted", err)
	}
	assertCheckpointVerification(t, f, store, id, "verified")
	if d.verifies != 1 {
		t.Fatal("proof repaired by new comparison")
	}
}
func TestPGCloneCheckpointPostgresVerificationRequiresAdoption(t *testing.T) {
	f, store, _, id := checkpointIsolationFixture(t)
	if _, err := store.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresVerification(t.Context(), f.lease, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unisolated fork verified", err)
	}
}
func TestPGCloneCheckpointPostgresVerificationLateFailureAndMigration(t *testing.T) {
	f, store, d, id := checkpointVerificationFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006325_clone_checkpoint_postgres_verification.sql")
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
	runtime, err := migrations.FS.ReadFile("20261011232006327_clone_checkpoint_postgres_runtime.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(runtime), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), `create function reject_checkpoint_verification() returns trigger language plpgsql as $$ begin if NEW.state='verified' then raise exception 'injected verification failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_checkpoint_verification before update on project_environment_clone_checkpoint_postgres_verification for each row execute function reject_checkpoint_verification()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d); err == nil {
		t.Fatal("expected final write failure")
	}
	assertCheckpointVerification(t, f, store, id, "requested")
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "clone_checkpoint_postgres_verification_down_no_ownership" {
		t.Fatal("downgrade lost verification authority", err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger reject_checkpoint_verification on project_environment_clone_checkpoint_postgres_verification; drop function reject_checkpoint_verification()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d)
	if err != nil || d.verifies != 1 {
		t.Fatal("final-write recovery repeated comparison", err)
	}
	assertCheckpointVerification(t, f, store, id, "verified")
}

func TestPGCloneCheckpointPostgresVerificationSubstitutedOwner(t *testing.T) {
	f, store, d, id := checkpointVerificationFixture(t)
	in, err := store.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresVerification(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	if in.Plan.Isolation.OwnerID == "" || in.Plan.IsolationSHA256 == "" || in.Plan.Isolation.ProviderSnapshotID == "" {
		t.Fatal("incomplete immutable comparison plan")
	}
	if _, err = f.pool.Exec(t.Context(), `update project_environment_clone_checkpoint_postgres_verification set plan=jsonb_set(plan,'{Isolation,OwnerID}','"foreign"'::jsonb) where operation_id=$1 and source_database_id=$2`, f.lease.Operation.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.srv.verifyCheckpointPostgres(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("substituted fork dispatched", err)
	}
	if d.verifies != 0 {
		t.Fatal("read substituted owner")
	}
}
