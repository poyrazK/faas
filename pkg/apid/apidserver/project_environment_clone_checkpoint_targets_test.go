//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointTargetLostReplyStore struct {
	*checkpointReleaseLostReplyStore
	loseTargetClaim, loseTargetFinish bool
}

func (s *checkpointTargetLostReplyStore) ClaimProjectEnvironmentCloneCheckpointObjectTarget(ctx context.Context, lease state.ProjectEnvironmentCloneLease, sourceID string, retention state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ProjectEnvironmentCloneCheckpointObjectTarget, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointObjectTarget(ctx, lease, sourceID, retention)
	if err == nil && s.loseTargetClaim {
		s.loseTargetClaim = false
		return state.ProjectEnvironmentCloneCheckpointObjectTarget{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointTargetLostReplyStore) FinishProjectEnvironmentCloneCheckpointObjectTarget(ctx context.Context, lease state.ProjectEnvironmentCloneLease, sourceID string, observed state.ProjectEnvironmentCloneCheckpointObjectTargetObservation, retention state.ProjectEnvironmentCloneCheckpointReleaseObservation) (state.ObjectBucket, error) {
	bucket, err := s.PgStore.FinishProjectEnvironmentCloneCheckpointObjectTarget(ctx, lease, sourceID, observed, retention)
	if err == nil && s.loseTargetFinish {
		s.loseTargetFinish = false
		return state.ObjectBucket{}, objectstorage.ErrUnavailable
	}
	return bucket, err
}

type checkpointTargetTestDriver struct {
	*checkpointReleaseTestDriver
	f            *cloneCoordinatorFixture
	creates      int
	loseCreate   bool
	native       map[string]state.ProjectEnvironmentCloneCheckpointObjectTargetObservation
	edit         func(*state.ProjectEnvironmentCloneCheckpointObjectTargetObservation)
	beforeFinish func()
}

func (d *checkpointTargetTestDriver) CreateCheckpointObjectTarget(ctx context.Context, plan state.ProjectEnvironmentCloneCheckpointObjectTargetPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_object_targets where operation_id=$1 and source_bucket_id=$2", plan.OperationID, plan.SourceBucketID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("native creation before durable claim", status, err)
	}
	d.creates++
	d.native[plan.TargetBucketID] = state.ProjectEnvironmentCloneCheckpointObjectTargetObservation{RequestID: state.ProjectEnvironmentCloneCheckpointObjectTargetRequestID(plan), PlanSHA256: state.ProjectEnvironmentCloneCheckpointObjectTargetPlanSHA256(plan), OwnerToken: plan.OperationID, NativeResourceID: plan.PhysicalName, State: "ready", Private: true, Empty: true, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if d.loseCreate {
		d.loseCreate = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointTargetTestDriver) ObserveCheckpointObjectTarget(_ context.Context, plan state.ProjectEnvironmentCloneCheckpointObjectTargetPlan) (state.ProjectEnvironmentCloneCheckpointObjectTargetObservation, error) {
	observed, ok := d.native[plan.TargetBucketID]
	if !ok {
		return observed, objectstorage.ErrNotFound
	}
	if d.edit != nil {
		d.edit(&observed)
	}
	if d.beforeFinish != nil {
		d.beforeFinish()
	}
	return observed, nil
}
func checkpointTargetFixture(t *testing.T, setup ...func(*cloneCoordinatorFixture, state.ObjectBucket)) (*cloneCoordinatorFixture, *checkpointTargetLostReplyStore, *checkpointTargetTestDriver) {
	t.Helper()
	f, base, release, _ := checkpointReleaseFixture(t, setup...)
	store := &checkpointTargetLostReplyStore{checkpointReleaseLostReplyStore: base}
	f.srv.store = statetest.NewPg(store)
	driver := &checkpointTargetTestDriver{checkpointReleaseTestDriver: release, f: &f, native: map[string]state.ProjectEnvironmentCloneCheckpointObjectTargetObservation{}}
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil {
		t.Fatal(err)
	}
	return &f, store, driver
}
func assertCheckpointTargetsPrivate(t *testing.T, f *cloneCoordinatorFixture, store *checkpointTargetLostReplyStore, wantReady bool) {
	t.Helper()
	for _, resource := range f.lease.Operation.Resources {
		if resource.Kind != "object_storage" {
			continue
		}
		var appID string
		if err := f.pool.QueryRow(t.Context(), "select app_id::text from object_buckets where id=$1", resource.TargetID).Scan(&appID); err != nil {
			t.Fatal(err)
		}
		bucket, err := store.PgStore.ProjectEnvironmentCloneObjectBucketForLease(t.Context(), f.lease, appID, resource.SourceID, resource.TargetID)
		if err != nil || bucket.PublicRead || bucket.ServeAt != "" || (bucket.State == "ready") != wantReady {
			t.Fatal("private target state", bucket.ID, bucket.State, err)
		}
		if _, err := store.PgStore.GetObjectBucket(t.Context(), bucket.AccountID, bucket.AppID, bucket.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("target published", err)
		}
	}
}

func TestPGCloneCheckpointTargetCreationLostNativeReply(t *testing.T) {
	f, store, driver := checkpointTargetFixture(t)
	driver.loseCreate = true
	var err error
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if !errors.Is(err, objectstorage.ErrUnavailable) || driver.creates != 1 {
		t.Fatal("lost native reply", err)
	}
	assertCheckpointTargetsPrivate(t, f, store, false)
	// Reopened source configuration cannot replace any target or manifest.
	if _, err := f.pool.Exec(t.Context(), "update object_buckets set public_read=true where id=$1", f.lease.Operation.Resources[3].SourceID); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	before := f.lease.Operation
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if err != nil || driver.creates != 2 || driver.captures != 1 || driver.releases != 1 || !reflect.DeepEqual(before, f.lease.Operation) {
		t.Fatal("original target recovery", driver.creates, err)
	}
	assertCheckpointTargetsPrivate(t, f, store, true)
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if err != nil || driver.creates != 2 {
		t.Fatal("ready targets recreated", err)
	}
}

func TestPGCloneCheckpointTargetCreationLostClaimDoesNotDispatch(t *testing.T) {
	f, store, driver := checkpointTargetFixture(t)
	store.loseTargetClaim = true
	var err error
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if !errors.Is(err, objectstorage.ErrUnavailable) || driver.creates != 0 {
		t.Fatal("claim uncertainty authorized IO", err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if !errors.Is(err, objectstorage.ErrNotFound) || driver.creates != 0 {
		t.Fatal("unknown request redispatched", err)
	}
	assertCheckpointTargetsPrivate(t, f, store, false)
	raw, err := migrations.FS.ReadFile("20261011232006318_clone_checkpoint_object_targets.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "clone_checkpoint_object_targets_down_no_ownership" {
		t.Fatal("downgrade forgot target dispatch", err)
	}
}

func TestPGCloneCheckpointTargetCreationLostCommitReply(t *testing.T) {
	f, store, driver := checkpointTargetFixture(t)
	store.loseTargetFinish = true
	var err error
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if err != nil || driver.creates != 2 {
		t.Fatal("lost commit recreated original", err)
	}
	assertCheckpointTargetsPrivate(t, f, store, true)
}

func TestPGCloneCheckpointTargetCreationRejectsForeignEvidence(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointObjectTargetObservation){
		"owner": func(o *state.ProjectEnvironmentCloneCheckpointObjectTargetObservation) {
			o.OwnerToken = uuid.NewString()
		},
		"plan": func(o *state.ProjectEnvironmentCloneCheckpointObjectTargetObservation) { o.PlanSHA256 = "foreign-plan" },
		"request": func(o *state.ProjectEnvironmentCloneCheckpointObjectTargetObservation) {
			o.RequestID = "foreign-request"
		},
		"resource": func(o *state.ProjectEnvironmentCloneCheckpointObjectTargetObservation) {
			o.NativeResourceID = "foreign-bucket"
		},
		"public":   func(o *state.ProjectEnvironmentCloneCheckpointObjectTargetObservation) { o.Private = false },
		"nonempty": func(o *state.ProjectEnvironmentCloneCheckpointObjectTargetObservation) { o.Empty = false },
		"time": func(o *state.ProjectEnvironmentCloneCheckpointObjectTargetObservation) {
			o.CreatedAt = time.Now().UTC().Add(-time.Hour)
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f, store, driver := checkpointTargetFixture(t)
			driver.edit = edit
			var err error
			f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("invalid native proof accepted", err)
			}
			assertCheckpointTargetsPrivate(t, f, store, false)
			driver.edit = nil
			f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
			if err != nil || driver.creates != 2 {
				t.Fatal("proof recovery recreated bucket", err)
			}
		})
	}
}

func TestPGCloneCheckpointTargetCreationGenericRecoveryExcluded(t *testing.T) {
	f, store, driver := checkpointTargetFixture(t)
	lease, targets, err := f.srv.checkpointObjectTargetsForLease(t.Context(), f.lease, driver)
	if err != nil {
		t.Fatal(err)
	}
	f.lease = lease
	for _, target := range targets {
		for _, recovery := range []bool{false, true} {
			var err error
			if recovery {
				_, err = store.PgStore.ClaimObjectBucketRecovery(t.Context(), target.AccountID, target.AppID, target.ID, uuid.NewString(), "provisioning")
			} else {
				_, err = store.PgStore.ClaimObjectBucket(t.Context(), target.AccountID, target.AppID, target.ID, uuid.NewString(), "provisioning")
			}
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("generic native creation admitted", err)
			}
		}
	}
	due, err := store.PgStore.DueObjectBuckets(t.Context(), true, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range due {
		if bucket.EnvironmentCloneOperationID == f.lease.Operation.ID {
			t.Fatal("private checkpoint target scheduled for generic creation")
		}
	}
}

func TestPGCloneCheckpointTargetCreationLateFailureRollsBack(t *testing.T) {
	f, store, driver := checkpointTargetFixture(t)
	if _, err := f.pool.Exec(t.Context(), `create function reject_checkpoint_target_ready() returns trigger language plpgsql as $$ begin if NEW.state='ready' and OLD.state='provisioning' and NEW.environment_clone_operation_id is not null then raise exception 'injected target commit failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_checkpoint_target_ready before update on object_buckets for each row execute function reject_checkpoint_target_ready()`); err != nil {
		t.Fatal(err)
	}
	var err error
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if err == nil {
		t.Fatal("expected final bucket update failure")
	}
	var status string
	if err := f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_object_targets where operation_id=$1", f.lease.Operation.ID).Scan(&status); err != nil || status != "requested" {
		t.Fatal("creation receipt escaped rollback", status, err)
	}
	assertCheckpointTargetsPrivate(t, f, store, false)
	if _, err := f.pool.Exec(t.Context(), "drop trigger reject_checkpoint_target_ready on object_buckets; drop function reject_checkpoint_target_ready()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if err != nil || driver.creates != 2 {
		t.Fatal("rollback recovery recreated original", err)
	}
	assertCheckpointTargetsPrivate(t, f, store, true)
}

func TestPGCloneCheckpointTargetCreationStaleWorker(t *testing.T) {
	f, _, driver := checkpointTargetFixture(t)
	old := f.lease
	driver.beforeFinish = func() {
		driver.beforeFinish = nil
		reclaimCommonCheckpointWorker(t, f)
	}
	_, err := f.srv.prepareCheckpointObjectTargets(t.Context(), old, driver)
	if !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale target worker committed", err)
	}
	var status string
	if err := f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_object_targets where operation_id=$1", f.lease.Operation.ID).Scan(&status); err != nil || status != "requested" {
		t.Fatal("stale receipt committed", status, err)
	}
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if err != nil || driver.creates != 2 {
		t.Fatal("handoff recreated target", err)
	}
}

func TestPGCloneCheckpointTargetCreationRetentionLostAfterNativeIO(t *testing.T) {
	f, store, driver := checkpointTargetFixture(t)
	driver.beforeFinish = func() {
		driver.beforeFinish = nil
		driver.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
			o.RetainedUntil = time.Now().Add(time.Minute)
		}
	}
	var err error
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if !errors.Is(err, state.ErrConflict) || driver.creates != 1 {
		t.Fatal("expired original retention accepted", err)
	}
	driver.editRelease = nil
	assertCheckpointTargetsPrivate(t, f, store, false)
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, driver)
	if err != nil || driver.creates != 2 {
		t.Fatal("retention recovery recreated target", err)
	}
}

func TestPGCloneCheckpointTargetCreationMigrationRoundTrip(t *testing.T) {
	f, _, _ := checkpointTargetFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006318_clone_checkpoint_object_targets.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), "DROP TABLE project_environment_clone_checkpoint_object_retirements,project_environment_clone_checkpoint_object_verification,project_environment_clone_checkpoint_object_copies"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal("empty target journal downgrade", err)
	}
	if _, err = tx.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal("target journal reapply", err)
	}
	if _, err = tx.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal("reapplied target journal downgrade", err)
	}
}
