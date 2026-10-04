//go:build !no_pg

// adr: 583
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

func (p *cloneSnapshotProvider) RestoreSnapshot(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreRequest) (managedpostgres.SnapshotRestoreObservation, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	p.forkCreates++
	if p.beforeForkCreate != nil {
		if err := p.beforeForkCreate(ctx, r); err != nil {
			return managedpostgres.SnapshotRestoreObservation{}, err
		}
	}
	if p.forkActual.ProviderResourceID != "" {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrConflict
	}
	p.forkDefinition, p.forkRequest = d, r
	p.forkActual = managedpostgres.SnapshotRestoreObservation{ProviderResourceID: "source-project/restored-branch", ProviderSnapshotID: r.ProviderSnapshotID, SourceDataResourceID: d.DataResourceID,
		PointInTime: r.Snapshot.PointInTime, SnapshotCreatedAt: p.actual.CreatedAt, TargetCreatedAt: time.Now().UTC().Truncate(time.Microsecond), Restored: !p.forkPending}
	if p.loseForkCreation {
		p.loseForkCreation = false
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrUnavailable
	}
	return p.forkActual, nil
}

func (p *cloneSnapshotProvider) FindSnapshotRestore(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotRestoreRequest) (managedpostgres.SnapshotRestoreObservation, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	p.forkFinds++
	if p.hideFork || p.forkActual.ProviderResourceID == "" {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrNotFound
	}
	if r.ExpectedTargetResourceID != "" && r.ExpectedTargetResourceID != p.forkActual.ProviderResourceID {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrConflict
	}
	r.ExpectedTargetResourceID = ""
	if d != p.forkDefinition || r != p.forkRequest {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrConflict
	}
	return p.forkActual, nil
}

type cloneSnapshotRestoreFailureStore struct {
	*state.PgStore
	loseReservation, loseClaim, failRecord, loseRecord bool
}

func (s *cloneSnapshotRestoreFailureStore) ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, limit int) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	r, err := s.PgStore.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, l, id, limit)
	if err == nil && s.loseReservation {
		s.loseReservation = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, errors.New("reservation reply lost")
	}
	return r, err
}

func (s *cloneSnapshotRestoreFailureStore) ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresSnapshotRestore, bool, error) {
	r, dispatch, err := s.PgStore.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, l, id)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, false, errors.New("dispatch reply lost")
	}
	return r, dispatch, err
}

func (s *cloneSnapshotRestoreFailureStore) RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (state.ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if s.failRecord {
		s.failRecord = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, errors.New("record unavailable")
	}
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, l, id, o)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return state.ProjectEnvironmentClonePostgresSnapshotRestore{}, errors.New("record reply lost")
	}
	return r, err
}

func cloneSnapshotRestoreWorkerFixture(t *testing.T, major ...int) (cloneCoordinatorFixture, *cloneSnapshotRestoreFailureStore, *cloneSnapshotProvider, string) {
	t.Helper()
	f, _, provider, sourceID := cloneSnapshotWorkerFixture(t, major...)
	var err error
	f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneSnapshotRestoreFailureStore{PgStore: f.store.PgStore}
	f.srv.store = store
	provider.beforeForkCreate = func(ctx context.Context, r managedpostgres.SnapshotRestoreRequest) error {
		receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, f.lease, sourceID)
		if err != nil {
			return err
		}
		if receipt.State != "requested" || receipt.RequestStartedAt.IsZero() || receipt.TargetOwnerID != r.ResourceID || r.ExpectedTargetResourceID != "" {
			return state.ErrConflict
		}
		return nil
	}
	return f, store, provider, sourceID
}

func TestPGClonePostgresSnapshotRestoreWorkerRecoversOwnedFork(t *testing.T) {
	f, store, p, sourceID := cloneSnapshotRestoreWorkerFixture(t)
	store.loseReservation = true
	var complete bool
	var err error
	f.lease, complete, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err == nil || complete || p.forkCreates != 0 {
		t.Fatalf("lost reservation dispatched: complete=%v err=%v", complete, err)
	}
	p.loseForkCreation, p.hideFork = true, true
	f.lease, complete, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || complete || p.forkCreates != 1 {
		t.Fatalf("unknown restore: complete=%v err=%v", complete, err)
	}
	f.lease, complete, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || complete || p.forkCreates != 1 {
		t.Fatalf("invisible fork repeated POST: complete=%v err=%v", complete, err)
	}
	p.hideFork = false
	store.failRecord = true
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err == nil || p.forkCreates != 1 {
		t.Fatalf("failed record lost intent: %v", err)
	}
	store.loseRecord = true
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err == nil || p.forkCreates != 1 {
		t.Fatalf("lost record acknowledgement: %v", err)
	}
	receipt, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID)
	if err != nil || receipt.State != "restored" || receipt.TargetProviderResourceID != p.forkActual.ProviderResourceID {
		t.Fatalf("durable target: %+v %v", receipt, err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, complete, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil || !complete || p.forkCreates != 1 || p.deadlineMissing || f.lease.Operation.Status != state.CloneOperationCapturing {
		t.Fatalf("recovered native storage: complete=%v err=%v", complete, err)
	}
	if f.lease.Operation.Resources[0].TargetID != "" {
		t.Fatal("native storage proof published a catalogue target")
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("storage restore published environment: %v", err)
	}
}

func TestPGClonePostgresSnapshotRestoreWorkerDoesNotRepeatUnknownDispatch(t *testing.T) {
	f, store, p, sourceID := cloneSnapshotRestoreWorkerFixture(t)
	store.loseClaim = true
	var err error
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err == nil || p.forkCreates != 0 {
		t.Fatalf("unknown committed dispatch reached provider: %v", err)
	}
	var complete bool
	f.lease, complete, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || complete || p.forkCreates != 0 || p.forkFinds != 1 {
		t.Fatalf("redispatched uncertain intent: complete=%v err=%v", complete, err)
	}
	if r, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID); err != nil || r.State != "requested" || r.TargetProviderResourceID != "" {
		t.Fatalf("missing fork discarded authority: %+v %v", r, err)
	}
}

func TestPGClonePostgresSnapshotRestoreWorkerWaitsForCompletionAndRejectsWrongPhase(t *testing.T) {
	f, _, p, _ := cloneSnapshotRestoreWorkerFixture(t)
	p.forkPending = true
	lease, complete, err := f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil || complete || p.forkCreates != 1 {
		t.Fatalf("pending storage: complete=%v err=%v", complete, err)
	}
	f.lease = lease
	p.forkActual.Restored = true
	f.lease, complete, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil || !complete || p.forkCreates != 1 {
		t.Fatalf("storage completion: complete=%v err=%v", complete, err)
	}
	for _, phase := range []string{state.CloneOperationCopying, state.CloneOperationCompensating} {
		bad := f.lease
		bad.Operation.Status = phase
		before := p.forkFinds
		if _, _, err := f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), bad); !errors.Is(err, state.ErrConflict) || p.forkFinds != before || p.forkCreates != 1 {
			t.Fatalf("wrong %s reached provider: %v", phase, err)
		}
	}
}
