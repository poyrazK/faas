//go:build !no_pg

// adr:568
package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (p *cloneSnapshotProvider) DeleteSnapshotRestore(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreDeletionRequest) (managedpostgres.SnapshotRestoreDeletionObservation, error) {
	if err := p.validateForkDeletion(ctx, d, r); err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	if !p.forkDeleteIssued {
		p.forkDeleteIssued = true
		p.forkDeleteRequest = r
		p.forkDeletes++
	}
	if p.loseForkDelete {
		p.loseForkDelete = false
		return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrUnavailable
	}
	ids := []string{"delete-b", "delete-a"}
	if p.emptyForkDeleteReply {
		ids = nil
	}
	return managedpostgres.SnapshotRestoreDeletionObservation{ProviderResourceID: p.forkActual.ProviderResourceID, OperationIDs: ids}, nil
}

func (p *cloneSnapshotProvider) ObserveSnapshotRestoreDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreDeletionRequest) (managedpostgres.SnapshotRestoreDeletionObservation, error) {
	p.forkDeleteObservations++
	if err := p.validateForkDeletion(ctx, d, r); err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	if !p.forkDeleteIssued {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrUnavailable
	}
	return managedpostgres.SnapshotRestoreDeletionObservation{ProviderResourceID: p.forkActual.ProviderResourceID, OperationIDs: []string{"delete-a", "delete-b"}, Done: p.forkDeleteReady}, nil
}

func (p *cloneSnapshotProvider) validateForkDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreDeletionRequest) error {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if p.beforeForkDelete != nil {
		if err := p.beforeForkDelete(ctx, r); err != nil {
			return err
		}
	}
	request := r.Restore
	if request.ExpectedTargetResourceID != p.forkActual.ProviderResourceID || !r.SnapshotCreatedAt.Equal(p.forkActual.SnapshotCreatedAt) || !r.TargetCreatedAt.Equal(p.forkActual.TargetCreatedAt) {
		return managedpostgres.ErrConflict
	}
	request.ExpectedTargetResourceID = ""
	if d != p.forkDefinition || request != p.forkRequest || len(r.OperationIDs) > 0 && !slices.Equal(r.OperationIDs, []string{"delete-a", "delete-b"}) {
		return managedpostgres.ErrConflict
	}
	return nil
}

type cloneForkCleanupFailureStore struct {
	*cloneSnapshotRestoreFailureStore
	loseBegin, loseIdentity, failOperations, loseOperations, loseFinish bool
}

func (s *cloneForkCleanupFailureStore) BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	r, err := s.PgStore.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id)
	if err == nil && s.loseBegin {
		s.loseBegin = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, errors.New("cleanup intent reply lost")
	}
	return r, err
}

func (s *cloneForkCleanupFailureStore) RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx, l, id, o)
	if err == nil && s.loseIdentity {
		s.loseIdentity = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, errors.New("cleanup identity reply lost")
	}
	return r, err
}

func (s *cloneForkCleanupFailureStore) RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if s.failOperations {
		s.failOperations = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, errors.New("operation write unavailable")
	}
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, l, id, o)
	if err == nil && s.loseOperations {
		s.loseOperations = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, errors.New("operation write reply lost")
	}
	return r, err
}

func (s *cloneForkCleanupFailureStore) FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	r, err := s.PgStore.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, id, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, errors.New("retirement reply lost")
	}
	return r, err
}

func cloneForkCleanupWorkerFixture(t *testing.T, undispatched bool) (cloneCoordinatorFixture, *cloneForkCleanupFailureStore, *cloneSnapshotProvider, string) {
	t.Helper()
	f, original, p, sourceID := cloneSnapshotRestoreWorkerFixture(t)
	var err error
	if undispatched {
		_, err = original.ReserveProjectEnvironmentClonePostgresSnapshotRestore(t.Context(), f.lease, sourceID, 3)
	} else {
		p.loseForkCreation = true
		f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
		if errors.Is(err, managedpostgres.ErrUnavailable) {
			err = nil
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	op := f.lease.Operation
	f.lease.Operation, err = original.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneForkCleanupFailureStore{cloneSnapshotRestoreFailureStore: original}
	f.srv.store = store
	return f, store, p, sourceID
}

func TestPGClonePostgresSnapshotRestoreCleanupWorkerRetainsRecoveryAcrossLostReplies(t *testing.T) {
	f, store, p, sourceID := cloneForkCleanupWorkerFixture(t, false)
	var complete bool
	var err error
	store.loseBegin = true
	f.lease, complete, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	if err == nil || complete || p.forkDeletes != 0 {
		t.Fatalf("lost intent dispatched: %v %v", complete, err)
	}
	p.hideFork = true
	f.lease, complete, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || complete || p.forkDeletes != 0 {
		t.Fatalf("missing unknown fork retired: %v %v", complete, err)
	}
	p.hideFork = false
	store.loseIdentity = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	if err == nil || p.forkDeletes != 0 {
		t.Fatalf("lost identity dispatched DELETE: %v", err)
	}
	p.beforeForkDelete = func(ctx context.Context, r managedpostgres.SnapshotRestoreDeletionRequest) error {
		receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, f.lease, sourceID)
		if err != nil {
			return err
		}
		ids, err := receipt.DeletionOperationIDs()
		if err != nil || receipt.State != "deleting" || receipt.DeletionStartedAt.IsZero() || receipt.TargetProviderResourceID != r.Restore.ExpectedTargetResourceID || !slices.Equal(ids, r.OperationIDs) {
			return state.ErrConflict
		}
		return nil
	}
	p.loseForkDelete = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.forkDeletes != 1 {
		t.Fatalf("lost DELETE reply: %v", err)
	}
	store.failOperations = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	if err == nil || p.forkDeletes != 1 || p.forkDeleteObservations != 0 {
		t.Fatalf("failed operation write lost intent: %v", err)
	}
	store.loseOperations = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	if err == nil || p.forkDeletes != 1 || p.forkDeleteObservations != 0 {
		t.Fatalf("lost operation acknowledgement: %v", err)
	}
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || p.deletes != 0 || p.forkDeletes != 1 || p.forkDeleteObservations != 1 {
		t.Fatalf("pending fork discarded source: %v", err)
	}
	if receipt, err := store.ProjectEnvironmentClonePostgresSnapshotForLease(t.Context(), f.lease, sourceID); err != nil || receipt.State != "retained" {
		t.Fatalf("pending fork lost source snapshot: %+v %v", receipt, err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p.forkDeleteReady, store.loseFinish = true, true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	if err == nil || p.forkDeletes != 1 || p.forkDeleteObservations != 2 {
		t.Fatalf("lost committed retirement reply: %v", err)
	}
	before := p.forkDeleteObservations
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || p.forkDeleteObservations != before || p.deletes != 1 || p.forkCreates != 1 || p.deadlineMissing {
		t.Fatalf("retirement replay redispatched or lost source cleanup: %v", err)
	}
	if receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID); err != nil || receipt.State != "deleted" {
		t.Fatalf("terminal receipt lost after source cleanup: %+v %v", receipt, err)
	}
}

func TestPGClonePostgresSnapshotRestoreCleanupWorkerPinsDiscoveredOperationsBeforeTerminalRead(t *testing.T) {
	f, store, p, sourceID := cloneForkCleanupWorkerFixture(t, false)
	p.beforeForkDelete = func(ctx context.Context, r managedpostgres.SnapshotRestoreDeletionRequest) error {
		receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, f.lease, sourceID)
		if err != nil {
			return err
		}
		ids, err := receipt.DeletionOperationIDs()
		if err != nil || !slices.Equal(ids, r.OperationIDs) {
			return state.ErrConflict
		}
		return nil
	}
	p.emptyForkDeleteReply, p.forkDeleteReady = true, true
	lease, complete, err := f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	f.lease = lease
	if err != nil || !complete || p.forkDeletes != 1 || p.forkDeleteObservations != 2 || p.deletes != 0 {
		t.Fatalf("discovered operation set lacked pinned terminal read: %v %v", complete, err)
	}
	if receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID); err != nil || receipt.State != "deleted" {
		t.Fatalf("independent proof not retired: %+v %v", receipt, err)
	}
}

func TestPGClonePostgresSnapshotRestoreCleanupWorkerClosesUndispatchedReservationLocally(t *testing.T) {
	f, store, p, sourceID := cloneForkCleanupWorkerFixture(t, true)
	f.srv.managedPostgres = nil
	lease, complete, err := f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), f.lease)
	f.lease = lease
	if err != nil || !complete || p.forkCreates != 0 || p.forkDeletes != 0 || p.forkFinds != 0 || p.forkDeleteObservations != 0 {
		t.Fatalf("undispatched local retirement: %v %v", complete, err)
	}
	if receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID); err != nil || receipt.State != "deleted" || !receipt.RequestStartedAt.IsZero() {
		t.Fatalf("local retirement receipt: %+v %v", receipt, err)
	}
	bad := f.lease
	bad.Operation.Status = state.CloneOperationCapturing
	if _, _, err := f.srv.cleanupProjectEnvironmentClonePostgresSnapshotRestores(t.Context(), bad); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture phase retired fork: %v", err)
	}
}
