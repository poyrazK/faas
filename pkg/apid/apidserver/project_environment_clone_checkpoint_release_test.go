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

type checkpointReleaseTestDriver struct {
	*checkpointAdoptionTestDriver
	releases     int
	loseRelease  bool
	terminal     state.ProjectEnvironmentCloneCheckpointReleaseObservation
	editRelease  func(*state.ProjectEnvironmentCloneCheckpointReleaseObservation)
	beforeFinish func()
}

func (d *checkpointReleaseTestDriver) ReleaseCapturedSources(_ context.Context, plan cloneCommonCheckpointPlan, receipt cloneCommonCheckpointReceipt) error {
	d.releases++
	d.terminal = state.ProjectEnvironmentCloneCheckpointReleaseObservation{RequestID: state.ProjectEnvironmentCloneCheckpointReleaseRequestID(plan.OperationID),
		PlanSHA256: cloneCheckpointDigest(plan), ReceiptSHA256: cloneCheckpointDigest(receipt), CheckpointID: receipt.CheckpointID}
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i, source := range plan.Sources {
		resource := source.ProviderResourceID
		if source.Kind == "managed_postgres" {
			resource = source.DataResourceID
		}
		d.terminal.Sources = append(d.terminal.Sources, state.ProjectEnvironmentCloneCheckpointSourceRelease{Kind: source.Kind, ID: source.ID, CaptureIdentity: receipt.Sources[i].CaptureIdentity,
			OwnerToken: plan.OperationID, SourceResourceID: resource, TerminalState: "released", ReleasedAt: at})
	}
	// Mutating the supplied inputs must not change the durable release plan.
	plan.Sources[0].ID = "mutated-release-input"
	receipt.Sources[0].CaptureIdentity = "mutated-release-input"
	if d.loseRelease {
		return objectstorage.ErrUnavailable
	}
	return nil
}

func (d *checkpointReleaseTestDriver) ObserveCapturedSourceRelease(_ context.Context, plan cloneCommonCheckpointPlan, receipt cloneCommonCheckpointReceipt, retainUntil time.Time) (state.ProjectEnvironmentCloneCheckpointReleaseObservation, error) {
	if d.terminal.RequestID == "" {
		return state.ProjectEnvironmentCloneCheckpointReleaseObservation{}, objectstorage.ErrNotFound
	}
	if cloneCheckpointDigest(plan) != d.terminal.PlanSHA256 || cloneCheckpointDigest(receipt) != d.terminal.ReceiptSHA256 {
		return state.ProjectEnvironmentCloneCheckpointReleaseObservation{}, state.ErrConflict
	}
	out := d.terminal
	out.Sources = append([]state.ProjectEnvironmentCloneCheckpointSourceRelease(nil), out.Sources...)
	out.RetainedUntil = retainUntil.Add(time.Hour)
	if d.editRelease != nil {
		d.editRelease(&out)
	}
	if d.beforeFinish != nil {
		d.beforeFinish()
	}
	return out, nil
}

type checkpointReleaseLostReplyStore struct {
	*cloneCaptureBarrierFailureStore
	loseClaim, loseFinish bool
}

func (s *checkpointReleaseLostReplyStore) ClaimProjectEnvironmentCloneCheckpointRelease(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointRelease, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointRelease(ctx, lease)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return state.ProjectEnvironmentCloneCheckpointRelease{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}

func (s *checkpointReleaseLostReplyStore) FinishProjectEnvironmentCloneCheckpointRelease(ctx context.Context, lease state.ProjectEnvironmentCloneLease, in state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneOperation, error) {
	op, err := s.PgStore.FinishProjectEnvironmentCloneCheckpointRelease(ctx, lease, in)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return state.ProjectEnvironmentCloneOperation{}, objectstorage.ErrUnavailable
	}
	return op, err
}

func checkpointReleaseFixture(t *testing.T, setup ...func(*cloneCoordinatorFixture, state.ObjectBucket)) (cloneCoordinatorFixture, *checkpointReleaseLostReplyStore, *checkpointReleaseTestDriver, []state.ObjectBucket) {
	t.Helper()
	f, base, provider, buckets := cloneCaptureBarrierFixture(t, setup...)
	provider.drained = true
	store := &checkpointReleaseLostReplyStore{cloneCaptureBarrierFailureStore: base}
	f.srv.store = statetest.NewPg(store)
	driver := &checkpointReleaseTestDriver{checkpointAdoptionTestDriver: &checkpointAdoptionTestDriver{commonCheckpointTestDriver: &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}}}
	return f, store, driver, buckets
}

func assertCheckpointReleaseHeld(t *testing.T, f cloneCoordinatorFixture, store *checkpointReleaseLostReplyStore, buckets []state.ObjectBucket) {
	t.Helper()
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), f.lease); err != nil {
		t.Fatal("configuration released on uncertainty", err)
	}
	for _, bucket := range buckets {
		if _, err := store.PgStore.BeginObjectBucketMutation(t.Context(), bucket, state.ObjectBucketMutationRequest); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
			t.Fatal("object source reopened", err)
		}
	}
	fences, err := store.ProjectEnvironmentClonePostgresWriteFencesForLease(t.Context(), f.lease)
	if err != nil || len(fences) != 1 || fences[0].State != "held" {
		t.Fatal("postgres source released on uncertainty", fences, err)
	}
}

func TestPGCloneCheckpointReleaseRecoversLostNativeReply(t *testing.T) {
	f, store, driver, buckets := checkpointReleaseFixture(t)
	driver.loseRelease = true
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) || driver.releases != 1 {
		t.Fatal("native release uncertainty", err)
	}
	assertCheckpointReleaseHeld(t, f, store, buckets)
	reclaimCommonCheckpointWorker(t, &f)
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil || driver.releases != 1 || driver.captures != 1 {
		t.Fatal("release dispatched twice", err)
	}
	release, err := store.PgStore.ProjectEnvironmentCloneCheckpointReleaseForLease(t.Context(), f.lease)
	if err != nil || release.State != "released" {
		t.Fatal("release not committed", release, err)
	}
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), f.lease); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("configuration still held", err)
	}
	for _, bucket := range buckets {
		ticket, err := store.PgStore.BeginObjectBucketMutation(t.Context(), bucket, state.ObjectBucketMutationRequest)
		if err != nil {
			t.Fatal("source writes not restored", err)
		}
		if err := store.PgStore.FinishObjectBucketMutation(t.Context(), ticket); err != nil {
			t.Fatal(err)
		}
	}
	fences, err := store.ProjectEnvironmentClonePostgresWriteFencesForLease(t.Context(), f.lease)
	if err != nil || len(fences) != 1 || fences[0].State != "released" || fences[0].RemoteTerminalState != "released" {
		t.Fatal("postgres release missing", fences, err)
	}
	resources := f.lease.Operation.Resources
	if resources[len(resources)-1].Kind != "data_checkpoint_release" {
		t.Fatal("private release receipt missing")
	}
	if _, err := store.PgStore.AdvanceProjectEnvironmentCloneOperation(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID,
		state.CloneOperationCapturing, state.CloneOperationCopying, f.lease.Operation.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("release enabled unqualified copying", err)
	}
}

func TestPGCloneCheckpointReleaseRecoversLostCommitAfterSourceMutation(t *testing.T) {
	f, store, driver, buckets := checkpointReleaseFixture(t)
	store.loseFinish = true
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("expected lost commit reply", err)
	}
	// Source configuration is writable after the successful release transaction.
	if _, err := f.pool.Exec(t.Context(), "update object_buckets set public_read=true where id=$1", buckets[0].ID); err != nil {
		t.Fatal("source not writable", err)
	}
	reclaimCommonCheckpointWorker(t, &f)
	before := f.lease.Operation
	nativeReads := driver.reads
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil || driver.releases != 1 || driver.captures != 1 || driver.reads != nativeReads || !reflect.DeepEqual(before, f.lease.Operation) {
		t.Fatal("recovery rebuilt mutable source plan", err)
	}
	// Replay cannot release a later capture's newly owned configuration guard.
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_configuration_guards set generation=generation+1,state='held',operation_id=$1,source_environment='dev',source_revision_hash=$2,held_at=clock_timestamp() where project_id=$3", f.lease.Operation.ID, f.lease.Operation.SourceRevisionHash, f.lease.Operation.ProjectID); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil {
		t.Fatal("released replay depended on fresh guard", err)
	}
	var stateValue string
	if err := f.pool.QueryRow(t.Context(), "select state from project_environment_clone_configuration_guards where project_id=$1", f.lease.Operation.ProjectID).Scan(&stateValue); err != nil || stateValue != "held" {
		t.Fatal("replay cleared a new hold", stateValue, err)
	}
}

func TestPGCloneCheckpointReleaseLostClaimDoesNotDispatch(t *testing.T) {
	f, store, driver, buckets := checkpointReleaseFixture(t)
	store.loseClaim = true
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) || driver.releases != 0 {
		t.Fatal("lost claim authorized dispatch", err)
	}
	reclaimCommonCheckpointWorker(t, &f)
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrNotFound) || driver.releases != 0 {
		t.Fatal("requested claim redispatched", err)
	}
	assertCheckpointReleaseHeld(t, f, store, buckets)
	raw, err := migrations.FS.ReadFile("20261011232006313_clone_checkpoint_releases.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("release downgrade missing")
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "clone_checkpoint_releases_down_no_ownership" {
		t.Fatal("downgrade discarded once-only release authority", err)
	}
	assertCheckpointReleaseHeld(t, f, store, buckets)

}

func TestPGCloneCheckpointReleaseRejectsUnverifiedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*state.ProjectEnvironmentCloneCheckpointReleaseObservation)
	}{
		{"wrong request", func(in *state.ProjectEnvironmentCloneCheckpointReleaseObservation) { in.RequestID = "replacement" }},
		{"wrong capture", func(in *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
			in.Sources[0].CaptureIdentity = "replacement"
		}},
		{"foreign owner", func(in *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
			in.Sources[0].OwnerToken = "foreign"
		}},
		{"wrong source", func(in *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
			in.Sources[0].SourceResourceID = "replacement"
		}},
		{"not terminal", func(in *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
			in.Sources[0].TerminalState = "held"
		}},
		{"missing source", func(in *state.ProjectEnvironmentCloneCheckpointReleaseObservation) { in.Sources = in.Sources[1:] }},
		{"expired retention", func(in *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
			in.RetainedUntil = time.Now().Add(-time.Second)
		}},
		{"old release", func(in *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
			in.Sources[0].ReleasedAt = time.Unix(1, 0).UTC()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, store, driver, buckets := checkpointReleaseFixture(t)
			driver.editRelease = tc.edit
			var err error
			f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("invalid release accepted", err)
			}
			assertCheckpointReleaseHeld(t, f, store, buckets)
			driver.editRelease = nil
			reclaimCommonCheckpointWorker(t, &f)
			f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
			if err != nil || driver.releases != 1 {
				t.Fatal("original evidence did not recover", err)
			}
		})
	}
}

func TestPGCloneCheckpointReleaseMissingCaptureCannotRelease(t *testing.T) {
	f, store, driver, buckets := checkpointReleaseFixture(t)
	driver.loseRelease = true
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_postgres_snapshots set provider_snapshot_id='replacement' where operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, &f)
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, state.ErrConflict) || driver.releases != 1 || driver.captures != 1 {
		t.Fatal("rewritten capture authorized release", err)
	}
	assertCheckpointReleaseHeld(t, f, store, buckets)
}

func TestPGCloneCheckpointReleaseStaleWorkerCannotCommit(t *testing.T) {
	f, store, driver, buckets := checkpointReleaseFixture(t)
	driver.beforeFinish = func() {
		if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=$1", f.lease.Operation.ID); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err == nil {
		t.Fatal("stale worker committed release")
	}
	driver.beforeFinish = nil
	reclaimCommonCheckpointWorker(t, &f)
	assertCheckpointReleaseHeld(t, f, store, buckets)
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil || driver.releases != 1 {
		t.Fatal("replacement worker could not settle original release", err)
	}
}

func TestPGCloneCheckpointReleaseLateFailureRollsBackAllHolds(t *testing.T) {
	f, store, driver, buckets := checkpointReleaseFixture(t)
	driver.beforeFinish = func() {
		// Reject the final inventory update after the release journal, PG/object
		// fences and configuration generation have all been written in the tx.
		if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_checkpoint_release_commit() RETURNS trigger LANGUAGE plpgsql AS $$
   BEGIN
    IF NEW.resources @> '[{"kind":"data_checkpoint_release"}]'::jsonb THEN
     RAISE EXCEPTION 'injected final release failure' USING ERRCODE='40001';
    END IF;
    RETURN NEW;
   END $$;
   CREATE TRIGGER reject_checkpoint_release_commit BEFORE UPDATE OF resources ON project_environment_clone_operations
    FOR EACH ROW EXECUTE FUNCTION reject_checkpoint_release_commit();`); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err == nil {
		t.Fatal("injected final failure did not stop release")
	}
	assertCheckpointReleaseHeld(t, f, store, buckets)
	journal, err := store.PgStore.ProjectEnvironmentCloneCheckpointReleaseForLease(t.Context(), f.lease)
	if err != nil || journal.State != "requested" || journal.ObservationSHA256 != "" {
		t.Fatal("partial release journal committed", journal, err)
	}
	if _, err := f.pool.Exec(t.Context(), "DROP TRIGGER reject_checkpoint_release_commit ON project_environment_clone_operations; DROP FUNCTION reject_checkpoint_release_commit();"); err != nil {
		t.Fatal(err)
	}
	driver.beforeFinish = nil
	reclaimCommonCheckpointWorker(t, &f)
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil || driver.releases != 1 {
		t.Fatal("original native release did not recover after rollback", err)
	}
}

func TestPGCloneCheckpointTargetsUseReleasedOriginals(t *testing.T) {
	f, store, driver, buckets := checkpointReleaseFixture(t)
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "update object_buckets set public_read=true where id=$1", buckets[0].ID); err != nil {
		t.Fatal(err)
	}
	before := f.lease.Operation
	reads := driver.reads
	var targets []state.ObjectBucket
	f.lease, targets, err = f.srv.checkpointObjectTargetsForLease(t.Context(), f.lease, driver)
	if err != nil || len(targets) != len(buckets) || !reflect.DeepEqual(before, f.lease.Operation) {
		t.Fatal("original target preflight", targets, err)
	}
	if driver.reads != reads || driver.releases != 1 || driver.captures != 1 {
		t.Fatal("target preflight recaptured or released sources")
	}
	for _, target := range targets {
		if target.State != "provisioning" || target.PublicRead || target.EnvironmentCloneOperationID != before.ID {
			t.Fatal("target no longer private", target.ID)
		}
		if _, err := store.PgStore.GetObjectBucket(t.Context(), target.AccountID, target.AppID, target.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("target exposed", err)
		}
	}
	driver.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
		o.RetainedUntil = time.Now().Add(time.Minute)
	}
	if _, targets, err = f.srv.checkpointObjectTargetsForLease(t.Context(), f.lease, driver); !errors.Is(err, state.ErrConflict) || targets != nil {
		t.Fatal("retention does not cover renewed lease", err)
	}
}

func TestPGCloneCheckpointTargetsRejectPendingRelease(t *testing.T) {
	f, _, driver, _ := checkpointReleaseFixture(t)
	driver.loseRelease = true
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, targets, err := f.srv.checkpointObjectTargetsForLease(t.Context(), f.lease, driver); !errors.Is(err, state.ErrConflict) || targets != nil {
		t.Fatal("uncertain release authorized targets", err)
	}
	if driver.releases != 1 || driver.captures != 1 {
		t.Fatal("preflight dispatched native work")
	}
}
