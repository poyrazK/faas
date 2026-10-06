//go:build !no_pg

// adr: 590
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

func (p *cloneSnapshotProvider) copyCleanupIdentity(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if p.hideCopy || p.copyActual.ProviderResourceID == "" {
		return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrNotFound
	}
	if r.ExpectedProviderResourceID != "" && (r.ExpectedProviderResourceID != p.copyActual.ProviderResourceID || !r.ExpectedCreatedAt.Equal(p.copyActual.CreatedAt)) {
		return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrConflict
	}
	r.ExpectedProviderResourceID, r.ExpectedCreatedAt = "", time.Time{}
	if d != p.copyDefinition || r != p.copyRequest {
		return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotCopyTargetIdentity{ProviderResourceID: p.copyActual.ProviderResourceID, CreatedAt: p.copyActual.CreatedAt, Deleted: p.copyDeleteIssued && !p.copyDeletePending}, nil
}

func (p *cloneSnapshotProvider) DiscoverSnapshotCopyTargetForCleanup(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	p.copyCleanupFinds++
	return p.copyCleanupIdentity(ctx, d, r)
}

func (p *cloneSnapshotProvider) DeleteSnapshotCopyTarget(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	actual, err := p.copyCleanupIdentity(ctx, d, r)
	if err != nil {
		return actual, err
	}
	if p.beforeCopyDelete != nil {
		if err := p.beforeCopyDelete(ctx, r); err != nil {
			return managedpostgres.SnapshotCopyTargetIdentity{}, err
		}
	}
	if !p.copyDeleteIssued {
		p.copyDeleteIssued = true
		p.copyDeletes++
	}
	if p.loseCopyDelete {
		p.loseCopyDelete = false
		return managedpostgres.SnapshotCopyTargetIdentity{}, managedpostgres.ErrUnavailable
	}
	actual.Deleted = false // The acknowledgement never supplies terminal proof.
	return actual, nil
}

func (p *cloneSnapshotProvider) ObserveSnapshotCopyTargetDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetIdentity, error) {
	p.copyDeletionObservations++
	return p.copyCleanupIdentity(ctx, d, r)
}

type cloneCopyCleanupFailureStore struct {
	*cloneCopyTargetFailureStore
	loseBegin, loseIdentity, loseFinish bool
}

func (s *cloneCopyCleanupFailureStore) BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	r, err := s.PgStore.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, l, id)
	if err == nil && s.loseBegin {
		s.loseBegin = false
		return state.ProjectEnvironmentClonePostgresCopyTarget{}, errors.New("committed cleanup intent reply lost")
	}
	return r, err
}
func (s *cloneCopyCleanupFailureStore) RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresCopyTargetDeletion) (state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(ctx, l, id, o)
	if err == nil && s.loseIdentity {
		s.loseIdentity = false
		return state.ProjectEnvironmentClonePostgresCopyTarget{}, errors.New("committed cleanup identity reply lost")
	}
	return r, err
}
func (s *cloneCopyCleanupFailureStore) FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresCopyTargetDeletion) (state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	r, err := s.PgStore.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, l, id, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return state.ProjectEnvironmentClonePostgresCopyTarget{}, errors.New("committed retirement reply lost")
	}
	return r, err
}

func cloneCopyCleanupCompensation(t *testing.T, f *cloneCoordinatorFixture, s *cloneCopyCleanupFailureStore) {
	t.Helper()
	op := f.lease.Operation
	var err error
	f.lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	f.srv.store = s
}

func TestPGClonePostgresSnapshotCopyTargetCleanupRecoversUnknownCreationAndLostReplies(t *testing.T) {
	f, original, p, sourceID := cloneCopyTargetWorkerFixture(t)
	p.loseCopyCreation = true
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.copyCreates != 1 {
		t.Fatalf("unknown creation: %v", err)
	}
	store := &cloneCopyCleanupFailureStore{cloneCopyTargetFailureStore: original}
	cloneCopyCleanupCompensation(t, &f, store)
	store.loseBegin = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err == nil || p.copyDeletes != 0 || p.copyCleanupFinds != 0 {
		t.Fatalf("lost intent dispatched: %v", err)
	}
	p.hideCopy = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.copyDeletes != 0 {
		t.Fatalf("unknown absence retired project: %v", err)
	}
	p.hideCopy = false
	store.loseIdentity = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err == nil || p.copyDeletes != 0 {
		t.Fatalf("lost identity pin dispatched DELETE: %v", err)
	}
	p.beforeCopyDelete = func(ctx context.Context, r managedpostgres.SnapshotCopyTargetRequest) error {
		row, err := store.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, f.lease, sourceID)
		if err != nil {
			return err
		}
		if row.State != "deleting" || row.DeletionStartedAt.IsZero() || row.ProviderResourceID != r.ExpectedProviderResourceID || !row.ProviderCreatedAt.Equal(r.ExpectedCreatedAt) || row.TargetDatabaseID != r.ResourceID {
			return state.ErrConflict
		}
		return nil
	}
	p.loseCopyDelete = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.copyDeletes != 1 || p.copyDeletionObservations != 0 {
		t.Fatalf("lost DELETE reply: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", f.lease.Operation.AccountID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("unknown deletion released quota: %d %v", count, err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.loseFinish = true
	f.lease, _, err = f.srv.cleanupProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err == nil || p.copyDeletes != 1 || p.copyDeletionObservations != 1 {
		t.Fatalf("lost retirement commit: %v", err)
	}
	r, err := store.ProjectEnvironmentClonePostgresCopyTargetForLease(t.Context(), f.lease, sourceID)
	if err != nil || r.State != "retired" || r.DeletionObservedAt.IsZero() {
		t.Fatalf("durable retirement: %+v %v", r, err)
	}
	ioBefore := p.copyCleanupFinds + p.copyDeletes + p.copyDeletionObservations
	var complete bool
	f.lease, complete, err = f.srv.cleanupProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err != nil || !complete || ioBefore != p.copyCleanupFinds+p.copyDeletes+p.copyDeletionObservations || p.copyCreates != 1 || p.deadlineMissing {
		t.Fatalf("retired replay performed provider IO: %v %v", complete, err)
	}
}

func TestPGClonePostgresSnapshotCopyTargetCleanupCoordinatorOrdersInputRetirement(t *testing.T) {
	f, original, p, sourceID := cloneCopyTargetWorkerFixture(t)
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneCopyCleanupFailureStore{cloneCopyTargetFailureStore: original}
	cloneCopyCleanupCompensation(t, &f, store)
	p.copyDeletePending = true
	p.forkDeleteReady = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || p.copyDeletes != 1 || p.copyDeletionObservations != 1 || p.forkDeletes != 0 || p.deletes != 0 {
		t.Fatalf("pending target released immutable input: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", f.lease.Operation.AccountID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("pending target released quota: %d %v", count, err)
	}
	// Cleanup must use frozen ownership even if production configuration changes.
	if _, err := f.pool.Exec(t.Context(), "update managed_postgres_databases set restore_window_seconds=0,storage_limit_bytes=123456 where id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	p.beforeForkDelete = func(ctx context.Context, _ managedpostgres.SnapshotRestoreDeletionRequest) error {
		r, err := store.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, f.lease, sourceID)
		if err == nil && r.State != "retired" {
			err = state.ErrConflict
		}
		return err
	}
	p.copyDeletePending = false
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || p.forkDeletes != 1 || p.deletes != 1 || p.copyCreates != 1 {
		t.Fatalf("ordered input disposal: %v", err)
	}
	r, err := store.ProjectEnvironmentClonePostgresCopyTargetForLease(t.Context(), f.lease, sourceID)
	if err != nil || r.State != "retired" {
		t.Fatalf("retired target lost after input deletion: %+v %v", r, err)
	}
	if err := f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", f.lease.Operation.AccountID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cleanup released production quota: %d %v", count, err)
	}
}

func TestPGClonePostgresSnapshotCopyTargetCleanupUndispatchedReservationUsesNoProviderIO(t *testing.T) {
	f, original, p, sourceID := cloneCopyTargetWorkerFixture(t)
	r, _, err := original.ReserveProjectEnvironmentClonePostgresCopyTarget(t.Context(), f.lease, sourceID, 3)
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneCopyCleanupFailureStore{cloneCopyTargetFailureStore: original}
	cloneCopyCleanupCompensation(t, &f, store)
	var complete bool
	f.lease, complete, err = f.srv.cleanupProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err != nil || !complete || p.copyCreates+p.copyCleanupFinds+p.copyDeletes+p.copyDeletionObservations != 0 {
		t.Fatalf("undispatched reservation used provider: %v %v", complete, err)
	}
	r, err = store.ProjectEnvironmentClonePostgresCopyTargetForLease(t.Context(), f.lease, sourceID)
	if err != nil || r.State != "retired" || !r.DeletionStartedAt.IsZero() || r.ProviderResourceID != "" {
		t.Fatalf("local retirement: %+v %v", r, err)
	}
}
