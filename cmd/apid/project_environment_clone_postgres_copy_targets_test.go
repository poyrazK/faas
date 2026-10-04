//go:build !no_pg

// adr: 569
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

func (p *cloneSnapshotProvider) PrepareSnapshotCopyTarget(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetObservation, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	p.copyCreates++
	if p.beforeCopyCreate != nil {
		if err := p.beforeCopyCreate(ctx, r); err != nil {
			return managedpostgres.SnapshotCopyTargetObservation{}, err
		}
	}
	if p.copyActual.ProviderResourceID != "" {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
	}
	p.copyDefinition, p.copyRequest = d, r
	p.copyActual = managedpostgres.SnapshotCopyTargetObservation{ProviderResourceID: "independent-project", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Spec: d.Spec, Prepared: !p.copyPending}
	if p.loseCopyCreation {
		p.loseCopyCreation = false
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrUnavailable
	}
	return p.copyActual, nil
}

func (p *cloneSnapshotProvider) FindSnapshotCopyTarget(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyTargetRequest) (managedpostgres.SnapshotCopyTargetObservation, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	p.copyFinds++
	if p.hideCopy || p.copyActual.ProviderResourceID == "" {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrNotFound
	}
	if r.ExpectedProviderResourceID != "" && (r.ExpectedProviderResourceID != p.copyActual.ProviderResourceID || !r.ExpectedCreatedAt.Equal(p.copyActual.CreatedAt)) {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
	}
	r.ExpectedProviderResourceID, r.ExpectedCreatedAt = "", time.Time{}
	if d != p.copyDefinition || r != p.copyRequest {
		return managedpostgres.SnapshotCopyTargetObservation{}, managedpostgres.ErrConflict
	}
	return p.copyActual, nil
}

type cloneCopyTargetFailureStore struct {
	*state.PgStore
	loseReservation, failRecord, loseRecord bool
}

func (s *cloneCopyTargetFailureStore) ReserveProjectEnvironmentClonePostgresCopyTarget(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, limit int) (state.ProjectEnvironmentClonePostgresCopyTarget, bool, error) {
	r, created, err := s.PgStore.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, l, id, limit)
	if err == nil && s.loseReservation {
		s.loseReservation = false
		return state.ProjectEnvironmentClonePostgresCopyTarget{}, false, errors.New("committed reservation reply lost")
	}
	return r, created, err
}

func (s *cloneCopyTargetFailureStore) RecordProjectEnvironmentClonePostgresCopyTarget(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresCopyTargetObservation) (state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	if s.failRecord {
		s.failRecord = false
		return state.ProjectEnvironmentClonePostgresCopyTarget{}, errors.New("record unavailable")
	}
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, l, id, o)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return state.ProjectEnvironmentClonePostgresCopyTarget{}, errors.New("committed observation reply lost")
	}
	return r, err
}

func cloneCopyTargetWorkerFixture(t *testing.T) (cloneCoordinatorFixture, *cloneCopyTargetFailureStore, *cloneSnapshotProvider, string) {
	t.Helper()
	f, _, p, sourceID := cloneSnapshotRestoreWorkerFixture(t)
	var err error
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneCopyTargetFailureStore{PgStore: f.store.PgStore}
	f.srv.store = store
	p.beforeCopyCreate = func(ctx context.Context, r managedpostgres.SnapshotCopyTargetRequest) error {
		target, err := store.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, f.lease, sourceID)
		if err != nil {
			return err
		}
		capture, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, f.lease, sourceID)
		if err != nil {
			return err
		}
		if target.State != "requested" || target.RequestStartedAt.IsZero() || r.ResourceID != target.TargetDatabaseID || r.ExpectedProviderResourceID != "" || capture.State != "adopted" || target.CaptureDatabaseID != capture.AdoptedDatabaseID || r.Capture.ExpectedTargetResourceID != capture.TargetProviderResourceID || !r.CaptureCreatedAt.Equal(capture.TargetCreatedAt) {
			return state.ErrConflict
		}
		return nil
	}
	return f, store, p, sourceID
}

func TestPGClonePostgresSnapshotCopyTargetWorkerRecoversPrivateIndependentOwner(t *testing.T) {
	f, store, p, sourceID := cloneCopyTargetWorkerFixture(t)
	// Edits to today's production configuration cannot replace frozen input.
	if _, err := f.pool.Exec(t.Context(), "update managed_postgres_databases set restore_window_seconds=0,storage_limit_bytes=123456 where id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	store.loseReservation = true
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err == nil || p.copyCreates != 0 {
		t.Fatalf("lost reservation dispatched: %v", err)
	}
	p.loseCopyCreation, p.hideCopy = true, true
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.copyCreates != 1 {
		t.Fatalf("unknown project outcome: %v", err)
	}
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.copyCreates != 1 {
		t.Fatalf("invisible project repeated POST: %v", err)
	}
	p.hideCopy = false
	store.failRecord = true
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err == nil || p.copyCreates != 1 {
		t.Fatalf("failed observation lost owner: %v", err)
	}
	store.loseRecord = true
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err == nil || p.copyCreates != 1 {
		t.Fatalf("lost committed observation: %v", err)
	}
	target, err := store.ProjectEnvironmentClonePostgresCopyTargetForLease(t.Context(), f.lease, sourceID)
	if err != nil || target.State != "prepared" || target.ProviderResourceID != p.copyActual.ProviderResourceID || p.copyDefinition.Spec.StorageLimitBytes == 123456 || p.copyDefinition.Spec.RestoreWindowSeconds == 0 {
		t.Fatalf("frozen independent owner: %+v %v", target, err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var prepared bool
	f.lease, prepared, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err != nil || !prepared || p.copyCreates != 1 || p.forkCreates != 1 || p.creates != 1 || p.deadlineMissing || f.lease.Operation.Resources[0].TargetID != "" {
		t.Fatalf("worker handoff: prepared=%v err=%v", prepared, err)
	}
	if _, err := f.srv.managedPostgres.Get(t.Context(), f.lease.Operation.AccountID, target.TargetDatabaseID); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("empty project became customer visible: %v", err)
	}
	if _, err := f.srv.managedPostgres.Reconcile(t.Context(), f.lease.Operation.AccountID, target.TargetDatabaseID); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("PITR provisioner claimed independent target: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("preparation published stage: %v", err)
	}
	op := f.lease.Operation
	f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	p.copyDeletePending = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || p.forkDeletes != 0 || p.deletes != 0 {
		t.Fatalf("active copy target lost immutable input: %v", err)
	}
}

func TestPGClonePostgresSnapshotCopyTargetWorkerKeepsPendingProjectUnready(t *testing.T) {
	f, store, p, sourceID := cloneCopyTargetWorkerFixture(t)
	p.copyPending = true
	var prepared bool
	var err error
	f.lease, prepared, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err != nil || prepared || p.copyCreates != 1 {
		t.Fatalf("pending preparation: %v %v", prepared, err)
	}
	target, err := store.ProjectEnvironmentClonePostgresCopyTargetForLease(t.Context(), f.lease, sourceID)
	if err != nil || target.State != "preparing" || target.ProviderResourceID == "" {
		t.Fatalf("pending project lost identity: %+v %v", target, err)
	}
	p.copyActual.Prepared = true
	f.lease, prepared, err = f.srv.prepareProjectEnvironmentClonePostgresCopyTargets(t.Context(), f.lease)
	if err != nil || !prepared || p.copyCreates != 1 || p.copyFinds != 1 {
		t.Fatalf("asynchronous preparation: %v %v", prepared, err)
	}
	databases, err := managedpostgres.NewPostgresStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	d, err := databases.Get(t.Context(), target.AccountID, target.TargetDatabaseID)
	if err != nil || d.State != managedpostgres.StateProvisioning || d.DataResourceID != "" || d.ObservedGeneration != 0 {
		t.Fatalf("project readiness authorized data access: %+v %v", d, err)
	}
}
