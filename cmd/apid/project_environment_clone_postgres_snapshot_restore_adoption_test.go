//go:build !no_pg

// adr:567
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneSnapshotAdoptionFailureStore struct {
	*cloneSnapshotRestoreFailureStore
	failAdoption, loseAdoption bool
}

func (s *cloneSnapshotAdoptionFailureStore) AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneDatabaseTarget, bool, error) {
	if s.failAdoption {
		s.failAdoption = false
		return state.ProjectEnvironmentCloneDatabaseTarget{}, false, errors.New("adoption unavailable")
	}
	r, created, err := s.PgStore.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, l, id)
	if err == nil && s.loseAdoption {
		s.loseAdoption = false
		return state.ProjectEnvironmentCloneDatabaseTarget{}, false, errors.New("committed adoption reply lost")
	}
	return r, created, err
}

func TestPGClonePostgresSnapshotRestoreAdoptionWorkerRecoversWithoutAnotherRestore(t *testing.T) {
	f, original, p, sourceID := cloneSnapshotRestoreWorkerFixture(t)
	store := &cloneSnapshotAdoptionFailureStore{cloneSnapshotRestoreFailureStore: original, failAdoption: true}
	f.srv.store = store
	var err error
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err == nil || p.forkCreates != 1 {
		t.Fatalf("unavailable adoption: %v", err)
	}
	if receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID); err != nil || receipt.State != "restored" || receipt.AdoptedDatabaseID != "" {
		t.Fatalf("failed adoption lost native fork: %+v %v", receipt, err)
	}
	store.loseAdoption = true
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err == nil || p.forkCreates != 1 {
		t.Fatalf("lost committed adoption: %v", err)
	}
	receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID)
	if err != nil || receipt.State != "adopted" || receipt.AdoptedDatabaseID != receipt.TargetOwnerID {
		t.Fatalf("durable adoption: %+v %v", receipt, err)
	}
	if _, err := f.srv.managedPostgres.Get(t.Context(), f.lease.Operation.AccountID, receipt.AdoptedDatabaseID); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("unisolated native target became customer visible: %v", err)
	}
	if _, err := f.srv.managedPostgres.Reconcile(t.Context(), f.lease.Operation.AccountID, receipt.AdoptedDatabaseID); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("ordinary reconcile claimed native target: %v", err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	finds := p.forkFinds
	var complete bool
	f.lease, complete, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil || !complete || p.forkCreates != 1 || p.forkFinds != finds || p.deadlineMissing || f.lease.Operation.Resources[0].TargetID != "" {
		t.Fatalf("adopted owner replay: complete=%v err=%v", complete, err)
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("adoption published environment: %v", err)
	}
}

func TestPGClonePostgresSnapshotRestoreAdoptionCleanupRetiresSameCatalogueOwner(t *testing.T) {
	f, store, p, sourceID := cloneSnapshotRestoreWorkerFixture(t)
	var err error
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID)
	if err != nil || receipt.State != "adopted" {
		t.Fatalf("native owner was not adopted: %+v %v", receipt, err)
	}
	op := f.lease.Operation
	f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	p.forkDeleteReady = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || p.forkCreates != 1 || p.forkDeletes != 1 || p.forkDeleteObservations != 1 || p.deletes != 1 {
		t.Fatalf("adopted fork retirement: %v", err)
	}
	if receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID); err != nil || receipt.State != "deleted" {
		t.Fatalf("retired adoption receipt: %+v %v", receipt, err)
	}
	databases, err := managedpostgres.NewPostgresStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if target, err := databases.Get(t.Context(), op.AccountID, receipt.AdoptedDatabaseID); err != nil || target.State != managedpostgres.StateDeleted || target.DataResourceID != "" {
		t.Fatalf("retired native catalogue: %+v %v", target, err)
	}
}
