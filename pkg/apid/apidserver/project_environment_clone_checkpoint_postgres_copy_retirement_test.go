//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkpointPgCopyRetirementFixture(t *testing.T, readerState, targetState string) (*cloneCoordinatorFixture, *state.PgStore, string) {
	t.Helper()
	ctx := t.Context()
	f, base, driver, id := checkpointPostgresRestoreFixture(t)
	s := base.PgStore
	var err error
	f.lease, _, err = f.srv.restoreCheckpointPostgres(ctx, f.lease, driver, 10)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.isolateCheckpointPostgres(ctx, f.lease, &checkpointIsolationTestDriver{checkpointPostgresRestoreTestDriver: driver, f: f})
	if err != nil {
		t.Fatal(err)
	}
	if readerState != "" {
		_, _, err = s.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, f.lease, id, 10)
		if err != nil {
			t.Fatal("reader reserve", err)
		}
		if readerState != "reserved" {
			_, _, err = s.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx, f.lease, id)
			if err != nil {
				t.Fatal(err)
			}
			if readerState != "requested" {
				_, err = s.RecordProjectEnvironmentClonePostgresCopyReader(ctx, f.lease, id, state.ProjectEnvironmentClonePostgresCopyReaderObservation{EndpointID: "original-reader", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Available: true})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if targetState != "" {
		_, _, err = s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, f.lease, id, 10)
		if err != nil {
			t.Fatal("copy target reserve", err)
		}
		if targetState != "reserved" {
			_, _, err = s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, f.lease, id)
			if err != nil {
				t.Fatal(err)
			}
			if targetState != "requested" {
				_, err = s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, f.lease, id, state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: "original-copy-target", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Prepared: targetState != "preparing"})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	op := f.lease.Operation
	f.lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	if readerState == "deleting" || readerState == "retired" {
		r, e := s.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, f.lease, id)
		if e != nil {
			t.Fatal(e)
		}
		if readerState == "retired" {
			if _, _, e = s.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, f.lease, id); e != nil {
				t.Fatal(e)
			}
			proof := state.ProjectEnvironmentClonePostgresCopyReaderDeletion{EndpointID: r.EndpointID, CreatedAt: r.EndpointCreatedAt, OperationIDs: []string{"legacy-reader-delete"}, Done: true}
			if _, e = s.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, f.lease, id, proof); e != nil {
				t.Fatal(e)
			}
			if _, e = s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, f.lease, id, proof); e != nil {
				t.Fatal(e)
			}
		}
	}
	if targetState == "deleting" || targetState == "retired" {
		r, e := s.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, f.lease, id)
		if e != nil {
			t.Fatal(e)
		}
		if targetState == "retired" {
			if _, e = s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, f.lease, id, state.ProjectEnvironmentClonePostgresCopyTargetDeletion{ProviderResourceID: r.ProviderResourceID, CreatedAt: r.ProviderCreatedAt, Done: true}); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("missing source compensation accepted", err)
	}
	compensateCaptureDependencySourcesTest(t, f)
	return f, s, id
}

func checkpointPgCopyRetirementProof(p state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation {
	o := state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlanSHA256(p), OwnerToken: p.Scope.OperationID, SourceDataResourceID: p.Scope.SourceDataResourceID, ProviderSnapshotID: p.Scope.ProviderSnapshotID, CaptureResourceID: p.Scope.CaptureProviderResourceID, NoNativeGrants: true, SourceUnchanged: true, SnapshotUnchanged: true, CaptureUnchanged: true, RetiredAt: time.Now().UTC().Truncate(time.Microsecond)}
	if p.Reader != nil {
		o.ReaderOwnerID, o.ReaderRequestID, o.ReaderEndpointID = p.Reader.OwnerID, p.Reader.OwnerID, p.Reader.EndpointID
		o.ReaderPermanentlyFenced, o.AllReaderOperationsPermanentlyClosed, o.NoOwnedReaders = true, true, true
	}
	if p.Target != nil {
		o.TargetOwnerID, o.TargetRequestID, o.TargetResourceID = p.Target.TargetDatabaseID, p.Target.TargetDatabaseID, p.Target.ProviderResourceID
		o.TargetPermanentlyFenced, o.AllWritersPermanentlyClosed, o.NoOwnedTargets = true, true, true
	}
	return o
}

func TestPGCloneCheckpointPgCopyRetirementOriginalStatesAndCaptureReplay(t *testing.T) {
	for _, pair := range [][2]string{{"reserved", "reserved"}, {"requested", "requested"}, {"observed", "preparing"}, {"observed", "prepared"}, {"deleting", "deleting"}, {"retired", "retired"}, {"", "requested"}, {"requested", ""}, {"", "reserved"}, {"observed", ""}} {
		t.Run(pair[0]+"/"+pair[1], func(t *testing.T) {
			f, s, id := checkpointPgCopyRetirementFixture(t, pair[0], pair[1])
			ctx := t.Context()
			before := cloneCheckpointDigest(f.lease.Operation)
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(ctx, f.lease, id); !errors.Is(err, state.ErrConflict) {
				t.Fatal("legacy deletion labels authorized parent cleanup", err)
			}
			in, err := s.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id)
			if err != nil || in.State != "planned" {
				t.Fatal("reserve", in.State, err)
			}
			again, err := s.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id)
			if err != nil || cloneCheckpointDigest(again) != cloneCheckpointDigest(in) {
				t.Fatal("reserve replay", err)
			}
			in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id)
			if err != nil || !dispatch || in.State != "requested" {
				t.Fatal("claim", err)
			}
			proof := checkpointPgCopyRetirementProof(in.Plan)
			if pair[0] == "requested" && pair[1] == "requested" {
				proof.ReaderEndpointID, proof.TargetResourceID = "discovered-reader", "discovered-copy-target"
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, proof); err != nil {
				t.Fatal("finish", err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, proof); err != nil {
				t.Fatal("lost finish replay", err)
			}
			changed := proof
			changed.RetiredAt = changed.RetiredAt.Add(time.Microsecond)
			if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed terminal proof", err)
			}
			in, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id)
			if err != nil || dispatch || in.State != "retired" {
				t.Fatal("terminal recovery", err)
			}
			if in.Plan.Target != nil {
				var status string
				if err = f.pool.QueryRow(ctx, "select state from managed_postgres_databases where id=$1", in.Plan.Target.TargetDatabaseID).Scan(&status); err != nil || status != "deleted" {
					t.Fatal("quota not released", status, err)
				}
			}
			f.srv.store = s
			targets := &checkpointPgTargetCompensationTestDriver{t: t, f: f}
			f.lease, err = f.srv.compensateCheckpointPostgresTargets(ctx, f.lease, targets)
			if err != nil {
				t.Fatal("parent target cleanup", err)
			}
			capture := &checkpointCaptureRetirementTestDriver{t: t, f: f}
			f.lease, err = f.srv.retireCheckpointCapture(ctx, f.lease, capture)
			if err != nil {
				t.Fatal("capture cleanup", err)
			}
			if _, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id); err != nil || dispatch {
				t.Fatal("replay after capture deletion", err)
			}
			if cloneCheckpointDigest(f.lease.Operation) != before {
				t.Fatal("whole-stage operation changed")
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, proof); err != nil {
				t.Fatal("terminal finish after capture deletion", err)
			}
			if _, err = f.pool.Exec(ctx, "update project_environment_clone_checkpoint_pg_copy_retirements set observation=jsonb_set(observation,'{AllWritersPermanentlyClosed}','false') where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id); err != nil {
				t.Fatal(err)
			}
			if in.Plan.Target != nil {
				if _, err = s.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(ctx, f.lease, id); !errors.Is(err, state.ErrConflict) {
					t.Fatal("corrupt copy proof replay", err)
				}
				if _, err = s.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(ctx, f.lease); !errors.Is(err, state.ErrConflict) {
					t.Fatal("corrupt copy proof masked by parent cleanup", err)
				}
			}
		})
	}
}

func TestPGCloneCheckpointPgCopyRetirementHandoffAndAtomicRollback(t *testing.T) {
	f, s, id := checkpointPgCopyRetirementFixture(t, "requested", "requested")
	ctx := t.Context()
	if _, err := s.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id); err != nil {
		t.Fatal(err)
	}
	in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id)
	if err != nil || !dispatch {
		t.Fatal(err)
	}
	old := f.lease
	reclaimCommonCheckpointWorker(t, f)
	if _, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, old, id); !errors.Is(err, state.ErrConflict) || dispatch {
		t.Fatal("stale lease", err)
	}
	in, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id)
	if err != nil || dispatch || in.State != "requested" {
		t.Fatal("lost claim redispatched", err)
	}
	proof := checkpointPgCopyRetirementProof(in.Plan)
	bad := proof
	bad.AllReaderOperationsPermanentlyClosed = false
	if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, bad); !errors.Is(err, state.ErrConflict) {
		t.Fatal("queued reader operations", err)
	}
	bad = proof
	bad.AllWritersPermanentlyClosed = false
	if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, bad); !errors.Is(err, state.ErrConflict) {
		t.Fatal("open writers", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(cancelled, f.lease, id, proof); err == nil {
		t.Fatal("cancelled finish")
	}
	if _, err = f.pool.Exec(ctx, `CREATE FUNCTION reject_copy_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.state='deleted' THEN RAISE EXCEPTION 'injected copy catalogue failure' USING ERRCODE='40001'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_copy_retirement BEFORE UPDATE ON managed_postgres_databases FOR EACH ROW EXECUTE FUNCTION reject_copy_retirement();`); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, proof); err == nil {
		t.Fatal("catalogue failure accepted")
	}
	in, dispatch, err = s.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id)
	if err != nil || dispatch || in.State != "requested" || in.ObservationSHA256 != "" {
		t.Fatal("rollback lost authority", err)
	}
	var status string
	if err = f.pool.QueryRow(ctx, "select state from project_environment_clone_postgres_copy_readers where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id).Scan(&status); err != nil || status != "requested" {
		t.Fatal("partial reader retirement", status, err)
	}
	if _, err = f.pool.Exec(ctx, "DROP TRIGGER reject_copy_retirement ON managed_postgres_databases; DROP FUNCTION reject_copy_retirement();"); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, proof); err != nil {
		t.Fatal("recovery", err)
	}
}

func TestPGCloneCheckpointPgCopyRetirementMigrationAndOwnership(t *testing.T) {
	f, s, id := checkpointPgCopyRetirementFixture(t, "observed", "prepared")
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261011232006388_clone_checkpoint_pg_copy_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal("empty migration replay", err)
		}
	}
	_ = tx.Rollback(ctx)
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"planned", "requested", "retired"} {
		if phase == "requested" {
			if _, _, err = s.ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "retired" {
			in, e := s.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(ctx, f.lease, id)
			if e != nil {
				t.Fatal(e)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(ctx, f.lease, id, checkpointPgCopyRetirementProof(in.Plan)); err != nil {
				t.Fatal(err)
			}
		}
		tx, err = f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, parts[1])
		_ = tx.Rollback(ctx)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "clone_checkpoint_pg_copy_retirements_down_no_ownership" {
			t.Fatal("owned downgrade", phase, err)
		}
	}
	if _, err = f.pool.Exec(ctx, "update project_environment_clone_postgres_copy_readers set endpoint_id='changed-reader' where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(ctx, f.lease, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed original identity", err)
	}
}
