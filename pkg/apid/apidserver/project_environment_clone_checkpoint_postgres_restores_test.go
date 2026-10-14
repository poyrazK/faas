//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointPostgresRestoreTestDriver struct {
	*checkpointReleaseTestDriver
	creates      int
	loseCreate   bool
	native       cloneCheckpointPostgresRestoreObservation
	edit         func(*cloneCheckpointPostgresRestoreObservation)
	afterObserve func()
}

func (d *checkpointPostgresRestoreTestDriver) RestoreCheckpointPostgres(_ context.Context, _ cloneCommonCheckpointPlan, snapshot state.ProjectEnvironmentClonePostgresSnapshot, receipt state.ProjectEnvironmentClonePostgresSnapshotRestore) error {
	d.creates++
	d.native = cloneCheckpointPostgresRestoreObservation{OwnerID: receipt.TargetOwnerID, Restore: state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation{ProviderSnapshotID: snapshot.ProviderSnapshotID, SourceDataResourceID: snapshot.SourceDataResourceID, TargetProviderResourceID: "checkpoint-fork-" + receipt.TargetOwnerID, CapturePoint: snapshot.CapturePoint.UTC(), SnapshotCreatedAt: snapshot.SnapshotCreatedAt.UTC(), TargetCreatedAt: time.Now().UTC().Truncate(time.Microsecond), Restored: true}}
	if d.loseCreate {
		d.loseCreate = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointPostgresRestoreTestDriver) ObserveCheckpointPostgresRestore(_ context.Context, _ cloneCommonCheckpointPlan, _ state.ProjectEnvironmentClonePostgresSnapshot, _ state.ProjectEnvironmentClonePostgresSnapshotRestore) (cloneCheckpointPostgresRestoreObservation, error) {
	if d.native.OwnerID == "" {
		return d.native, objectstorage.ErrNotFound
	}
	out := d.native
	if d.edit != nil {
		d.edit(&out)
	}
	if d.afterObserve != nil {
		d.afterObserve()
	}
	return out, nil
}

type checkpointPostgresRestoreFailureStore struct {
	*checkpointReleaseLostReplyStore
	loseRestoreClaim, loseRestoreRecord bool
}

func (s *checkpointPostgresRestoreFailureStore) ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx context.Context, lease state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresSnapshotRestore, bool, error) {
	receipt, dispatch, err := s.PgStore.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, id)
	if err == nil && s.loseRestoreClaim {
		s.loseRestoreClaim = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, false, objectstorage.ErrUnavailable
	}
	return receipt, dispatch, err
}
func (s *checkpointPostgresRestoreFailureStore) RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx context.Context, lease state.ProjectEnvironmentCloneLease, id string, observed state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	receipt, err := s.PgStore.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, id, observed)
	if err == nil && s.loseRestoreRecord {
		s.loseRestoreRecord = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, objectstorage.ErrUnavailable
	}
	return receipt, err
}
func checkpointPostgresRestoreFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointPostgresRestoreFailureStore, *checkpointPostgresRestoreTestDriver, string) {
	t.Helper()
	f, base, release, _ := checkpointReleaseFixture(t)
	store := &checkpointPostgresRestoreFailureStore{checkpointReleaseLostReplyStore: base}
	f.srv.store = statetest.NewPg(store)
	driver := &checkpointPostgresRestoreTestDriver{checkpointReleaseTestDriver: release}
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := ""
	for _, resource := range f.lease.Operation.Resources {
		if resource.Kind == "managed_postgres" {
			sourceID = resource.SourceID
		}
	}
	return &f, store, driver, sourceID
}
func TestPGCloneCheckpointPostgresRestoreOriginalRecovery(t *testing.T) {
	f, store, driver, sourceID := checkpointPostgresRestoreFixture(t)
	driver.loseCreate = true
	var err error
	f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) || driver.creates != 1 {
		t.Fatal("lost native restore", err)
	}
	if _, err := f.pool.Exec(t.Context(), "update managed_postgres_databases set state='provisioning' where id=$1", sourceID); err != nil {
		t.Fatal("source mutation", err)
	}
	reclaimCommonCheckpointWorker(t, f)
	before := f.lease.Operation
	var complete bool
	f.lease, complete, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if err != nil || !complete || driver.creates != 1 || driver.captures != 1 || driver.releases != 1 || !reflect.DeepEqual(before, f.lease.Operation) {
		t.Fatal("original restore recovery", complete, err)
	}
	receipt, err := store.PgStore.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID)
	if err != nil || receipt.State != "restored" || receipt.AdoptedDatabaseID != "" {
		t.Fatal("fork adopted or not restored", receipt.State, err)
	}
	if _, _, err := store.PgStore.AdoptProjectEnvironmentClonePostgresSnapshotRestore(t.Context(), f.lease, sourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("legacy adoption mutated checkpoint inventory", err)
	}
	if _, _, err := f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease); !errors.Is(err, errCloneCheckpointUnavailable) {
		t.Fatal("legacy restore admitted checkpoint", err)
	}
	f.lease, complete, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if err != nil || !complete || driver.creates != 1 {
		t.Fatal("fork recreated", err)
	}
}
func TestPGCloneCheckpointPostgresRestoreLostClaim(t *testing.T) {
	f, store, driver, _ := checkpointPostgresRestoreFixture(t)
	store.loseRestoreClaim = true
	var err error
	f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) || driver.creates != 0 {
		t.Fatal("unknown claim dispatched", err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrNotFound) || driver.creates != 0 {
		t.Fatal("unknown native request retried", err)
	}
}
func TestPGCloneCheckpointPostgresRestoreLostCommit(t *testing.T) {
	f, store, driver, _ := checkpointPostgresRestoreFixture(t)
	store.loseRestoreRecord = true
	var err error
	f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	var complete bool
	f.lease, complete, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if err != nil || !complete || driver.creates != 1 {
		t.Fatal("lost commit recreated fork", err)
	}
}
func TestPGCloneCheckpointPostgresRestoreRejectsEvidence(t *testing.T) {
	for name, edit := range map[string]func(*cloneCheckpointPostgresRestoreObservation){
		"owner":    func(o *cloneCheckpointPostgresRestoreObservation) { o.OwnerID = "foreign" },
		"snapshot": func(o *cloneCheckpointPostgresRestoreObservation) { o.Restore.ProviderSnapshotID = "replacement" },
		"source":   func(o *cloneCheckpointPostgresRestoreObservation) { o.Restore.SourceDataResourceID = "replacement" },
	} {
		t.Run(name, func(t *testing.T) {
			f, _, driver, _ := checkpointPostgresRestoreFixture(t)
			driver.edit = edit
			var err error
			f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
			if !errors.Is(err, state.ErrConflict) {
				t.Fatal("foreign fork accepted", err)
			}
			driver.edit = nil
			f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
			if err != nil || driver.creates != 1 {
				t.Fatal("original proof recovery", err)
			}
		})
	}
}
func TestPGCloneCheckpointPostgresRestoreRetentionLost(t *testing.T) {
	f, store, driver, sourceID := checkpointPostgresRestoreFixture(t)
	driver.afterObserve = func() {
		driver.afterObserve = nil
		driver.editRelease = func(o *state.ProjectEnvironmentCloneCheckpointReleaseObservation) {
			o.RetainedUntil = time.Now().Add(time.Minute)
		}
	}
	var err error
	f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired retention accepted", err)
	}
	receipt, err := store.PgStore.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID)
	if err != nil || receipt.State != "requested" {
		t.Fatal("restored committed without retention", err)
	}
	driver.editRelease = nil
	f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if err != nil || driver.creates != 1 {
		t.Fatal("retention recovery recreated fork", err)
	}
}
func TestPGCloneCheckpointPostgresRestoreStaleWorker(t *testing.T) {
	f, _, driver, _ := checkpointPostgresRestoreFixture(t)
	old := f.lease
	driver.afterObserve = func() { driver.afterObserve = nil; reclaimCommonCheckpointWorker(t, f) }
	if _, _, err := f.srv.restoreCheckpointPostgres(t.Context(), old, driver, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker committed restore", err)
	}
	var err error
	f.lease, _, err = f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if err != nil || driver.creates != 1 {
		t.Fatal("handoff recreated fork", err)
	}
}

func TestPGCloneCheckpointPostgresRestorePending(t *testing.T) {
	f, _, driver, _ := checkpointPostgresRestoreFixture(t)
	driver.edit = func(o *cloneCheckpointPostgresRestoreObservation) { o.Restore.Restored = false }
	lease, complete, err := f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10)
	if err != nil || complete || driver.creates != 1 {
		t.Fatal("pending fork treated as complete", err)
	}
	driver.edit = nil
	_, complete, err = f.srv.restoreCheckpointPostgres(t.Context(), lease, driver, 10)
	if err != nil || !complete || driver.creates != 1 {
		t.Fatal("pending fork recreated", err)
	}
}
func TestPGCloneCheckpointPostgresRestoreRequiresRelease(t *testing.T) {
	f, _, release, _ := checkpointReleaseFixture(t)
	driver := &checkpointPostgresRestoreTestDriver{checkpointReleaseTestDriver: release}
	driver.loseRelease = true
	var err error
	f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, _, err := f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 10); !errors.Is(err, state.ErrConflict) || driver.creates != 0 {
		t.Fatal("unreleased checkpoint restored", err)
	}
}
func TestPGCloneCheckpointPostgresRestoreQuotaBeforeIO(t *testing.T) {
	f, _, driver, _ := checkpointPostgresRestoreFixture(t)
	lease, _, err := f.srv.restoreCheckpointPostgres(t.Context(), f.lease, driver, 1)
	if !errors.Is(err, state.ErrQuotaExceeded) || driver.creates != 0 {
		t.Fatal("quota did not precede native restore", err)
	}
	if _, _, err := f.srv.restoreCheckpointPostgres(t.Context(), lease, driver, 10); err != nil || driver.creates != 1 {
		t.Fatal("quota recovery", err)
	}
}
