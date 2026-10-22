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

type checkpointObjectCopyTestStore struct {
	*checkpointTargetLostReplyStore
	loseClaim, loseFinish bool
}

func (s *checkpointObjectCopyTestStore) ClaimProjectEnvironmentCloneCheckpointObjectCopy(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectCopy, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointObjectCopy(ctx, l, id, r)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return in, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointObjectCopyTestStore) FinishProjectEnvironmentCloneCheckpointObjectCopy(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointObjectCopyObservation, r state.ProjectEnvironmentCloneCheckpointReleaseObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointObjectCopy(ctx, l, id, o, r)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointObjectCopyTestDriver struct {
	*checkpointTargetTestDriver
	calls     int
	loseReply bool
	proofs    map[string]state.ProjectEnvironmentCloneCheckpointObjectCopyObservation
	edit      func(*state.ProjectEnvironmentCloneCheckpointObjectCopyObservation)
}

func (d *checkpointObjectCopyTestDriver) CopyCheckpointObjects(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointObjectCopyPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_object_copies where operation_id=$1 and source_bucket_id=$2", p.Inventory.Target.OperationID, p.Inventory.Target.SourceBucketID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("copy before committed claim", status, err)
	}
	if _, exists := d.proofs[p.Inventory.Target.TargetBucketID]; exists {
		d.t.Fatal("redispatched original copy")
	}
	d.calls++
	d.proofs[p.Inventory.Target.TargetBucketID] = state.ProjectEnvironmentCloneCheckpointObjectCopyObservation{RequestID: state.ProjectEnvironmentCloneCheckpointObjectCopyRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointObjectCopyPlanSHA256(p), OwnerToken: p.Inventory.Target.OperationID, NativeResourceID: p.Inventory.Target.PhysicalName, Complete: true, MetadataComplete: true, UnsupportedObjectsAbsent: true, PinnedVersionsOnly: true, Private: true, WritersPermanentlyClosed: true, NoCredentials: true, TargetContentsUnchanged: true, ObjectCount: int64(len(p.Inventory.Objects)), BytesCopied: p.Inventory.TotalBytes, CopiedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseReply {
		d.loseReply = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointObjectCopyTestDriver) ObserveCheckpointObjectCopy(_ context.Context, p state.ProjectEnvironmentCloneCheckpointObjectCopyPlan, until time.Time) (state.ProjectEnvironmentCloneCheckpointObjectCopyObservation, error) {
	o, ok := d.proofs[p.Inventory.Target.TargetBucketID]
	if !ok {
		return o, objectstorage.ErrNotFound
	}
	o.ClosedThrough = until
	if d.edit != nil {
		d.edit(&o)
	}
	return o, nil
}
func checkpointObjectCopyFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointObjectCopyTestStore, *checkpointObjectCopyTestDriver) {
	t.Helper()
	f, base, target := checkpointTargetFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, target)
	if err != nil {
		t.Fatal(err)
	}
	store := &checkpointObjectCopyTestStore{checkpointTargetLostReplyStore: base}
	f.srv.store = statetest.NewPg(store)
	driver := &checkpointObjectCopyTestDriver{checkpointTargetTestDriver: target, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectCopyObservation{}}
	return f, store, driver
}
func TestPGCloneCheckpointObjectCopyRecovery(t *testing.T) {
	for _, lost := range []string{"native", "finish", "claim"} {
		t.Run(lost, func(t *testing.T) {
			f, s, d := checkpointObjectCopyFixture(t)
			d.loseReply = lost == "native"
			s.loseFinish = lost == "finish"
			s.loseClaim = lost == "claim"
			var err error
			f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
			if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("lost reply", err)
			}
			if lost == "claim" {
				if d.calls != 0 {
					t.Fatal("unknown claim dispatched")
				}
				f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
				if !errors.Is(err, objectstorage.ErrNotFound) || d.calls != 0 {
					t.Fatal("unknown request restarted", err)
				}
				return
			}
			reclaimCommonCheckpointWorker(t, f)
			f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal(err)
			}
			calls := d.calls
			f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
			if err != nil || d.calls != calls {
				t.Fatal("copied replay", err)
			}
			assertCheckpointTargetsPrivate(t, f, s.checkpointTargetLostReplyStore, true)
			verifier := &checkpointObjectVerificationTestDriver{checkpointTargetTestDriver: d.checkpointTargetTestDriver, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectVerificationObservation{}}
			f.lease, err = f.srv.verifyCheckpointObjects(t.Context(), f.lease, verifier)
			if err != nil {
				t.Fatal("copied target verification", err)
			}
			f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
			if err != nil || d.calls != calls {
				t.Fatal("copy replay after verification", err)
			}

			d.edit = func(o *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
				o.TargetContentsUnchanged = false
			}
			f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) || d.calls != calls {
				t.Fatal("changed target accepted", err)
			}
		})
	}
}
func TestPGCloneCheckpointObjectCopyMigration(t *testing.T) {
	f, _, d := checkpointObjectCopyFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006350_clone_checkpoint_object_copies.sql")
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
	f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
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
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_object_copies_down_no_ownership" {
		t.Fatal("owned verification downgrade", err)
	}
}

func TestPGCloneCheckpointObjectCopyFinishFences(t *testing.T) {
	for _, failure := range []string{"worker", "retention", "rollback"} {
		t.Run(failure, func(t *testing.T) {
			f, _, d := checkpointObjectCopyFixture(t)
			if failure == "rollback" {
				if _, err := f.pool.Exec(t.Context(), `create function reject_checkpoint_object_copies() returns trigger language plpgsql as $$ begin if NEW.state='copied' then raise exception 'injected verification commit failure' using errcode='40001'; end if; return NEW; end $$; create trigger reject_checkpoint_object_copies before update on project_environment_clone_checkpoint_object_copies for each row execute function reject_checkpoint_object_copies()`); err != nil {
					t.Fatal(err)
				}
			} else {
				d.edit = func(_ *state.ProjectEnvironmentCloneCheckpointObjectCopyObservation) {
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
			_, err := f.srv.copyCheckpointObjects(t.Context(), old, d)
			if err == nil {
				t.Fatal("failed verification committed")
			}
			var status string
			if err := f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_object_copies where operation_id=$1", f.lease.Operation.ID).Scan(&status); err != nil || status != "requested" {
				t.Fatal("receipt escaped fence", status, err)
			}
			if failure == "rollback" {
				if _, err := f.pool.Exec(t.Context(), "drop trigger reject_checkpoint_object_copies on project_environment_clone_checkpoint_object_copies; drop function reject_checkpoint_object_copies()"); err != nil {
					t.Fatal(err)
				}
			}
			d.editRelease = nil
			f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
			if err != nil || d.calls != 2 {
				t.Fatal("fence recovery restarted comparison", err, d.calls)
			}
		})
	}
}

func TestPGCloneCheckpointObjectCopyGatesVerificationAndDeadline(t *testing.T) {
	f, s, d := checkpointObjectCopyFixture(t)
	var id string
	for _, r := range f.lease.Operation.Resources {
		if r.Kind == "object_storage" {
			id = r.SourceID
			break
		}
	}
	if _, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectVerification(t.Context(), f.lease, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("verification before copy", err)
	}
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCopy(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	original := in.Plan.WriteUntil
	reclaimCommonCheckpointWorker(t, f)
	shorterRetention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointObjectCopy(t.Context(), f.lease, id, shorterRetention); !errors.Is(err, state.ErrConflict) || dispatch {
		t.Fatal("copy deadline outlasted replacement lease", err, dispatch)
	}
	f.lease, _, err = f.srv.checkpointObjectTargetsForLease(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := f.srv.checkpointTargetRetention(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointObjectCopy(t.Context(), f.lease, id, retention); !errors.Is(err, state.ErrConflict) || dispatch {
		t.Fatal("planned write window extended", err, dispatch)
	}
	in, err = s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCopy(t.Context(), f.lease, id)
	if err != nil || !in.Plan.WriteUntil.Equal(original) {
		t.Fatal("original deadline changed", err)
	}
}
