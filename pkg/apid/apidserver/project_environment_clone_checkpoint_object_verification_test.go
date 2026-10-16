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
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointObjectVerificationTestStore struct {
	*checkpointTargetLostReplyStore
	loseClaim, loseFinish bool
}

func (s *checkpointObjectVerificationTestStore) ClaimProjectEnvironmentCloneCheckpointObjectVerification(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectVerification, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointObjectVerification(ctx, l, id, r)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return in, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointObjectVerificationTestStore) FinishProjectEnvironmentCloneCheckpointObjectVerification(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointObjectVerification(ctx, l, id, o, r)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointObjectVerificationTestDriver struct {
	*checkpointTargetTestDriver
	calls     int
	loseReply bool
	proofs    map[string]state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation
	edit      func(*state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation)
}

func (d *checkpointObjectVerificationTestDriver) VerifyCheckpointObjects(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_object_verification where operation_id=$1 and source_bucket_id=$2", p.Target.OperationID, p.Target.SourceBucketID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("comparison before committed claim", status, err)
	}
	if _, exists := d.proofs[p.Target.TargetBucketID]; exists {
		d.t.Fatal("redispatched original comparison")
	}
	d.calls++
	d.proofs[p.Target.TargetBucketID] = state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation{RequestID: state.ProjectEnvironmentCloneCheckpointObjectVerificationRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointObjectVerificationPlanSHA256(p), OwnerToken: p.Target.OperationID, NativeResourceID: p.Target.PhysicalName, SourceSHA256: strings.Repeat("a", 64), TargetSHA256: strings.Repeat("a", 64), Complete: true, MetadataComplete: true, UnsupportedObjectsAbsent: true, ReadOnly: true, PinnedVersionsOnly: true, Private: true, AllWritersClosed: true, NoCredentials: true, TargetContentsUnchanged: true, ObjectCount: int64(len(p.Objects)), SourceReadBytes: p.TotalBytes, TargetReadBytes: p.TotalBytes, ComparedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseReply {
		d.loseReply = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointObjectVerificationTestDriver) ObserveCheckpointObjectVerification(_ context.Context, p state.ProjectEnvironmentCloneCheckpointObjectVerificationPlan, until time.Time) (state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation, error) {
	o, ok := d.proofs[p.Target.TargetBucketID]
	if !ok {
		return o, objectstorage.ErrNotFound
	}
	o.ClosedThrough = until
	if d.edit != nil {
		d.edit(&o)
	}
	return o, nil
}
func checkpointObjectVerificationFixture(t *testing.T, setup ...func(*cloneCoordinatorFixture, state.ObjectBucket)) (*cloneCoordinatorFixture, *checkpointObjectVerificationTestStore, *checkpointObjectVerificationTestDriver) {
	t.Helper()
	f, base, target := checkpointTargetFixture(t, setup...)
	var err error
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, target)
	if err != nil {
		t.Fatal(err)
	}
	store := &checkpointObjectVerificationTestStore{checkpointTargetLostReplyStore: base}
	f.srv.store = statetest.NewPg(store)
	driver := &checkpointObjectVerificationTestDriver{checkpointTargetTestDriver: target, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation{}}
	copyDriver := &checkpointObjectCopyTestDriver{checkpointTargetTestDriver: target, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectCopyObservation{}}
	f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, copyDriver)
	if err != nil {
		t.Fatal(err)
	}
	return f, store, driver
}
func TestPGCloneCheckpointObjectVerificationRecovery(t *testing.T) {
	for _, lost := range []string{"native", "finish", "claim"} {
		t.Run(lost, func(t *testing.T) {
			f, s, d := checkpointObjectVerificationFixture(t)
			d.loseReply = lost == "native"
			s.loseFinish = lost == "finish"
			s.loseClaim = lost == "claim"
			var err error
			f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
			if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("lost reply", err)
			}
			if lost == "claim" {
				if d.calls != 0 {
					t.Fatal("unknown claim dispatched")
				}
				f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
				if !errors.Is(err, objectstorage.ErrNotFound) || d.calls != 0 {
					t.Fatal("unknown request restarted", err)
				}
				return
			}
			reclaimCommonCheckpointWorker(t, f)
			f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			calls := d.calls
			f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
			if err != nil || d.calls != calls {
				t.Fatal("verified replay", err)
			}
			assertCheckpointTargetsPrivate(t, f, s.checkpointTargetLostReplyStore, true)
			d.edit = func(o *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
				o.TargetContentsUnchanged = false
			}
			f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) || d.calls != calls {
				t.Fatal("changed target accepted", err)
			}
		})
	}
}
func TestPGCloneCheckpointObjectVerificationMigration(t *testing.T) {
	f, _, d := checkpointObjectVerificationFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006346_clone_checkpoint_object_verification.sql")
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
			t.Fatal("empty verification migration", err)
		}
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_object_verification_down_no_ownership" {
		t.Fatal("owned verification downgrade", err)
	}
}

func TestPGCloneCheckpointObjectVerificationFinishFences(t *testing.T) {
	for _, failure := range []string{"worker", "retention", "rollback"} {
		t.Run(failure, func(t *testing.T) {
			f, _, d := checkpointObjectVerificationFixture(t)
			if failure == "rollback" {
				if _, err := f.pool.Exec(t.Context(), `create function reject_checkpoint_object_verification() returns trigger language plpgsql as $$ begin if NEW.state='verified' then raise exception 'injected verification commit failure' using errcode='40001'; end if; return NEW; end $$; create trigger reject_checkpoint_object_verification before update on project_environment_clone_checkpoint_object_verification for each row execute function reject_checkpoint_object_verification()`); err != nil {
					t.Fatal(err)
				}
			} else {
				d.edit = func(_ *state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation) {
					d.edit = nil
					if failure == "worker" {
						reclaimCommonCheckpointWorker(t, f)
					} else {
						d.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
							o.RetainedUntil = time.Now().Add(time.Minute)
						}
					}
				}
			}
			old := f.lease
			_, err := f.srv.verifyCheckpointObjects(t.Context(), old, d)
			if err == nil {
				t.Fatal("failed verification committed")
			}
			var status string
			if err := f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_object_verification where operation_id=$1", f.lease.Operation.ID).Scan(&status); err != nil || status != "requested" {
				t.Fatal("receipt escaped fence", status, err)
			}
			if failure == "rollback" {
				if _, err := f.pool.Exec(t.Context(), "drop trigger reject_checkpoint_object_verification on project_environment_clone_checkpoint_object_verification; drop function reject_checkpoint_object_verification()"); err != nil {
					t.Fatal(err)
				}
			}
			d.editRelease = nil
			f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, d)
			if err != nil || d.calls != 2 {
				t.Fatal("fence recovery restarted comparison", err, d.calls)
			}
		})
	}
}
