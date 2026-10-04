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

func (p *cloneSnapshotProvider) PrepareSnapshotCopyReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) (managedpostgres.SnapshotCopyReaderObservation, error) {
	p.readerCreates++
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if p.beforeReaderCreate != nil {
		if err := p.beforeReaderCreate(ctx, r); err != nil {
			return managedpostgres.SnapshotCopyReaderObservation{}, err
		}
	}
	if p.readerActual.EndpointID != "" {
		return managedpostgres.SnapshotCopyReaderObservation{}, managedpostgres.ErrConflict
	}
	p.readerDefinition, p.readerRequest = d, r
	p.readerActual = managedpostgres.SnapshotCopyReaderObservation{EndpointID: "ep-owned-reader", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Available: !p.readerPending}
	if p.loseReaderCreation {
		p.loseReaderCreation = false
		return managedpostgres.SnapshotCopyReaderObservation{}, managedpostgres.ErrUnavailable
	}
	return p.readerActual, nil
}

func (p *cloneSnapshotProvider) validateReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) error {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if r.ExpectedEndpointID != "" && (r.ExpectedEndpointID != p.readerActual.EndpointID || !r.ExpectedCreatedAt.Equal(p.readerActual.CreatedAt)) {
		return managedpostgres.ErrConflict
	}
	r.ExpectedEndpointID, r.ExpectedCreatedAt = "", time.Time{}
	if r != p.readerRequest || d != p.readerDefinition {
		return managedpostgres.ErrConflict
	}
	return nil
}

func (p *cloneSnapshotProvider) FindSnapshotCopyReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) (managedpostgres.SnapshotCopyReaderObservation, error) {
	p.readerFinds++
	if p.hideReader || p.readerActual.EndpointID == "" {
		return managedpostgres.SnapshotCopyReaderObservation{}, managedpostgres.ErrNotFound
	}
	if err := p.validateReader(ctx, d, r); err != nil {
		return managedpostgres.SnapshotCopyReaderObservation{}, err
	}
	return p.readerActual, nil
}

func (p *cloneSnapshotProvider) DeleteSnapshotCopyReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDeletionRequest) (managedpostgres.SnapshotCopyReaderDeletionObservation, error) {
	if err := p.validateReader(ctx, d, r.Reader); err != nil {
		return managedpostgres.SnapshotCopyReaderDeletionObservation{}, err
	}
	if p.beforeReaderDelete != nil {
		if err := p.beforeReaderDelete(ctx, r); err != nil {
			return managedpostgres.SnapshotCopyReaderDeletionObservation{}, err
		}
	}
	p.readerDeletes++
	if p.loseReaderDelete {
		p.loseReaderDelete = false
		return managedpostgres.SnapshotCopyReaderDeletionObservation{}, managedpostgres.ErrUnavailable
	}
	return managedpostgres.SnapshotCopyReaderDeletionObservation{EndpointID: p.readerActual.EndpointID, CreatedAt: p.readerActual.CreatedAt, OperationIDs: []string{"reader-delete-b", "reader-delete-a"}}, nil
}

func (p *cloneSnapshotProvider) ObserveSnapshotCopyReaderDeletion(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDeletionRequest) (managedpostgres.SnapshotCopyReaderDeletionObservation, error) {
	p.readerReads++
	if err := p.validateReader(ctx, d, r.Reader); err != nil {
		return managedpostgres.SnapshotCopyReaderDeletionObservation{}, err
	}
	o := managedpostgres.SnapshotCopyReaderDeletionObservation{EndpointID: p.readerActual.EndpointID, CreatedAt: p.readerActual.CreatedAt}
	if len(r.OperationIDs) > 0 {
		if !slices.Equal(r.OperationIDs, []string{"reader-delete-a", "reader-delete-b"}) {
			return o, managedpostgres.ErrConflict
		}
		o.OperationIDs, o.Done = r.OperationIDs, p.readerDeleteReady && p.readerAbsent
		return o, nil
	}
	if !p.forkDeleteReady || !p.readerAbsent || !slices.Equal(r.CaptureOperationIDs, []string{"delete-a", "delete-b"}) {
		return o, managedpostgres.ErrUnavailable
	}
	o.CaptureOperationIDs, o.Done = r.CaptureOperationIDs, true
	return o, nil
}

type cloneReaderFailureStore struct {
	*state.PgStore
	loseReservation, loseRequestClaim, failRecord, loseRecord bool
	loseBegin, loseCleanupClaim, loseProof, loseFinish        bool
}

func (s *cloneReaderFailureStore) ReserveProjectEnvironmentClonePostgresCopyReader(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, limit int) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	r, created, err := s.PgStore.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, l, id, limit)
	if err == nil && s.loseReservation {
		s.loseReservation = false
		return r, created, errors.New("committed reservation reply lost")
	}
	return r, created, err
}

func (s *cloneReaderFailureStore) ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	r, dispatch, err := s.PgStore.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx, l, id)
	if err == nil && s.loseRequestClaim {
		s.loseRequestClaim = false
		return r, dispatch, errors.New("committed creation dispatch reply lost")
	}
	return r, dispatch, err
}

func (s *cloneReaderFailureStore) RecordProjectEnvironmentClonePostgresCopyReader(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresCopyReaderObservation) (state.ProjectEnvironmentClonePostgresCopyReader, error) {
	if s.failRecord {
		s.failRecord = false
		return state.ProjectEnvironmentClonePostgresCopyReader{}, errors.New("observation unavailable")
	}
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresCopyReader(ctx, l, id, o)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return r, errors.New("committed observation reply lost")
	}
	return r, err
}

func (s *cloneReaderFailureStore) BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresCopyReader, error) {
	r, err := s.PgStore.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id)
	if err == nil && s.loseBegin {
		s.loseBegin = false
		return r, errors.New("committed cleanup intent reply lost")
	}
	return r, err
}

func (s *cloneReaderFailureStore) ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	r, dispatch, err := s.PgStore.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id)
	if err == nil && s.loseCleanupClaim {
		s.loseCleanupClaim = false
		return r, dispatch, errors.New("committed cleanup dispatch reply lost")
	}
	return r, dispatch, err
}

func (s *cloneReaderFailureStore) RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresCopyReaderDeletion) (state.ProjectEnvironmentClonePostgresCopyReader, error) {
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, l, id, o)
	if err == nil && s.loseProof {
		s.loseProof = false
		return r, errors.New("committed deletion proof reply lost")
	}
	return r, err
}

func (s *cloneReaderFailureStore) FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresCopyReaderDeletion) (state.ProjectEnvironmentClonePostgresCopyReader, error) {
	r, err := s.PgStore.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return r, errors.New("committed retirement reply lost")
	}
	return r, err
}

func cloneReaderWorkerFixture(t *testing.T, majors ...int) (cloneCoordinatorFixture, *cloneReaderFailureStore, *cloneSnapshotProvider, string) {
	t.Helper()
	f, _, p, sourceID := cloneSnapshotRestoreWorkerFixture(t, majors...)
	var err error
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneReaderFailureStore{PgStore: f.store.PgStore}
	f.srv.store = store
	p.beforeReaderCreate = func(ctx context.Context, r managedpostgres.SnapshotCopyReaderRequest) error {
		reader, err := store.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, f.lease, sourceID)
		if err == nil && (reader.State != "requested" || reader.OwnerID != r.ResourceID || reader.RequestStartedAt.IsZero() || !reader.RequestStartedAt.Equal(r.RequestedAt) ||
			reader.Scope.CaptureProviderResourceID != r.Capture.ExpectedTargetResourceID || !reader.Scope.CaptureCreatedAt.Equal(r.CaptureCreatedAt) || r.ExpectedEndpointID != "") {
			err = state.ErrConflict
		}
		return err
	}
	return f, store, p, sourceID
}

func cloneReaderWorkerGate(t *testing.T, f cloneCoordinatorFixture, p *cloneSnapshotProvider, enabled *bool) {
	t.Helper()
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "snapshot-worker"}}}, func(string) string { return "" },
		map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return p, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	f.srv.managedPostgres, err = managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{
		ProvisioningEnabled: func() bool { return *enabled }, ProvisioningAllowed: func(context.Context, string) bool { return *enabled }})
	if err != nil {
		t.Fatal(err)
	}
}

func cloneReaderCompensate(t *testing.T, f *cloneCoordinatorFixture, store *cloneReaderFailureStore) {
	t.Helper()
	op := f.lease.Operation
	var err error
	f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestPGClonePostgresSnapshotReaderWorkerRecoversUnknownCreationWithFrozenInput(t *testing.T) {
	f, store, p, sourceID := cloneReaderWorkerFixture(t)
	enabled := true
	cloneReaderWorkerGate(t, f, p, &enabled)
	if _, err := f.pool.Exec(t.Context(), "update managed_postgres_databases set restore_window_seconds=0,storage_limit_bytes=123456 where id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	store.loseReservation = true
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err == nil || p.readerCreates != 0 {
		t.Fatalf("lost reservation dispatched: %v", err)
	}
	p.loseReaderCreation, p.hideReader = true, true
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.readerCreates != 1 {
		t.Fatalf("unknown outcome: %v", err)
	}
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.readerCreates != 1 {
		t.Fatalf("unknown absence repeated creation: %v", err)
	}
	p.hideReader, enabled, store.failRecord = false, false, true
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err == nil || p.readerCreates != 1 {
		t.Fatalf("failed observation: %v", err)
	}
	store.loseRecord = true
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err == nil || p.readerCreates != 1 {
		t.Fatalf("lost observation reply: %v", err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var available bool
	f.lease, available, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	reader, readErr := store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	if err != nil || readErr != nil || !available || reader.EndpointID != p.readerActual.EndpointID || !reader.EndpointCreatedAt.Equal(p.readerActual.CreatedAt) ||
		p.readerDefinition.Spec.RestoreWindowSeconds == 0 || p.readerDefinition.Spec.StorageLimitBytes == 123456 || p.readerCreates != 1 || p.deadlineMissing {
		t.Fatalf("recovery replaced frozen input: %+v %v %v", reader, err, readErr)
	}
	if f.lease.Operation.Resources[0].TargetID != "" || f.lease.Operation.Resources[0].Status != "captured" {
		t.Fatal("compute metadata authorized dataset publication")
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("reader published an environment: %v", err)
	}
}

func TestPGClonePostgresSnapshotReaderWorkerPendingAvailabilityAndPreDispatchGate(t *testing.T) {
	f, store, p, sourceID := cloneReaderWorkerFixture(t)
	enabled := false
	cloneReaderWorkerGate(t, f, p, &enabled)
	reader, _, err := store.ReserveProjectEnvironmentClonePostgresCopyReader(t.Context(), f.lease, sourceID, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	actual, readErr := store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || readErr != nil || !actual.RequestStartedAt.IsZero() || actual.OwnerID != reader.OwnerID || p.readerCreates != 0 {
		t.Fatalf("disabled gate consumed dispatch: %+v %v %v", actual, err, readErr)
	}
	enabled, p.readerPending = true, true
	var available bool
	f.lease, available, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil || available || p.readerCreates != 1 {
		t.Fatalf("pending endpoint: %v %v", available, err)
	}
	p.readerActual.Available = true
	f.lease, available, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil || !available || p.readerCreates != 1 {
		t.Fatalf("availability polling: %v %v", available, err)
	}
	p.readerActual.CreatedAt = p.readerActual.CreatedAt.Add(time.Microsecond)
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrConflict) || p.readerCreates != 1 {
		t.Fatalf("replacement endpoint accepted: %v", err)
	}
}

func TestPGClonePostgresSnapshotReaderWorkerLostDispatchClaimRetainsUnknownOwner(t *testing.T) {
	f, store, p, sourceID := cloneReaderWorkerFixture(t)
	store.loseRequestClaim = true
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err == nil || p.readerCreates != 0 {
		t.Fatalf("lost claim dispatched POST: %v", err)
	}
	reader, err := store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	if err != nil || reader.RequestStartedAt.IsZero() {
		t.Fatalf("lost claim forgotten: %+v %v", reader, err)
	}
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.readerCreates != 0 {
		t.Fatalf("uncertain dispatch repeated POST: %v", err)
	}
	cloneReaderCompensate(t, &f, store)
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.forkDeletes != 0 || p.deletes != 0 || p.readerDeletes != 0 {
		t.Fatalf("unknown reader discarded discovery input: %v", err)
	}
}

func TestPGClonePostgresSnapshotReaderCleanupCoordinatesLostDeleteAndNativeQuotaHold(t *testing.T) {
	f, store, p, sourceID := cloneReaderWorkerFixture(t)
	var err error
	p.loseReaderCreation = true
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatal(err)
	}
	cloneReaderCompensate(t, &f, store)
	enabled := false
	cloneReaderWorkerGate(t, f, p, &enabled)
	p.hideReader = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || p.readerDeletes != 0 || p.forkDeletes != 0 {
		t.Fatalf("unknown reader absence lost capture: %v", err)
	}
	p.hideReader, store.loseRecord = false, true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if err == nil || p.readerDeletes != 0 || p.forkDeletes != 0 {
		t.Fatalf("lost cleanup identity dispatched: %v", err)
	}
	p.beforeReaderDelete = func(ctx context.Context, r managedpostgres.SnapshotCopyReaderDeletionRequest) error {
		reader, err := store.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, f.lease, sourceID)
		if err == nil && (reader.State != "deleting" || reader.CleanupDispatchedAt.IsZero() || !reader.CleanupDispatchedAt.Equal(r.RequestedAt) || reader.EndpointID != r.Reader.ExpectedEndpointID || reader.Available) {
			err = state.ErrConflict
		}
		return err
	}
	p.loseReaderDelete = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || p.readerDeletes != 1 || p.forkDeletes != 1 || p.deletes != 0 {
		t.Fatalf("lost reader DELETE prevented qualified parent progress: %v", err)
	}
	p.forkDeleteReady = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	capture, readErr := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(t.Context(), f.lease, sourceID)
	var held int
	quotaErr := f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", f.lease.Operation.AccountID).Scan(&held)
	if !errors.Is(err, errCloneCompensationUnavailable) || readErr != nil || quotaErr != nil || capture.State != "deleting" || held != 2 || p.deletes != 0 || p.readerDeletes != 1 {
		t.Fatalf("native deletion inferred endpoint absence or released quota: %+v held=%d %v %v %v", capture, held, err, readErr, quotaErr)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p.readerAbsent, store.loseProof = true, true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if err == nil || p.readerDeletes != 1 || p.deletes != 0 {
		t.Fatalf("lost retained proof released native catalogue: %v", err)
	}
	store.loseFinish = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	reader, readErr := store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	ids, idsErr := reader.CaptureDeletionOperationIDs()
	if err == nil || readErr != nil || idsErr != nil || reader.State != "retired" || !slices.Equal(ids, []string{"delete-a", "delete-b"}) || p.deletes != 0 {
		t.Fatalf("lost retirement proof: %+v %v %v %v", reader, err, readErr, idsErr)
	}
	ioBefore := p.readerFinds + p.readerDeletes + p.readerReads
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || p.deletes != 1 || p.readerDeletes != 1 || p.forkDeletes != 1 || ioBefore != p.readerFinds+p.readerDeletes+p.readerReads || p.deadlineMissing {
		t.Fatalf("retired replay repeated reader IO: %v", err)
	}
	if err := f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", f.lease.Operation.AccountID).Scan(&held); err != nil || held != 1 {
		t.Fatalf("qualified cleanup did not release capture quota: %d %v", held, err)
	}
}

func TestPGClonePostgresSnapshotReaderCleanupLostClaimUsesNoRepeatedDelete(t *testing.T) {
	f, store, p, sourceID := cloneReaderWorkerFixture(t)
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	cloneReaderCompensate(t, &f, store)
	store.loseBegin = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if err == nil || p.readerDeletes != 0 || p.forkDeletes != 0 {
		t.Fatalf("lost cleanup intent dispatched: %v", err)
	}
	store.loseCleanupClaim = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if err == nil || p.readerDeletes != 0 || p.forkDeletes != 0 {
		t.Fatalf("lost cleanup claim dispatched: %v", err)
	}
	p.forkDeleteReady, p.readerAbsent = true, true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	reader, readErr := store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	if !errors.Is(err, errCloneCompensationUnavailable) || readErr != nil || reader.State != "retired" || p.readerDeletes != 0 || p.forkDeletes != 1 || p.deletes != 1 {
		t.Fatalf("unknown DELETE dispatch repeated or lost native proof: %+v %v %v", reader, err, readErr)
	}
}

func TestPGClonePostgresSnapshotReaderCleanupRequiresStoredEndpointChainAndAbsence(t *testing.T) {
	f, store, p, sourceID := cloneReaderWorkerFixture(t)
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	cloneReaderCompensate(t, &f, store)
	store.loseProof = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	reader, readErr := store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	ids, idsErr := reader.DeletionOperationIDs()
	if err == nil || readErr != nil || idsErr != nil || !slices.Equal(ids, []string{"reader-delete-a", "reader-delete-b"}) || p.readerDeletes != 1 || p.readerReads != 0 || p.forkDeletes != 0 {
		t.Fatalf("lost chain acknowledgement observed or released native input: %+v %v %v %v", reader, err, readErr, idsErr)
	}
	p.readerDeleteReady, p.forkDeleteReady = true, true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	reader, readErr = store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	if !errors.Is(err, errCloneCompensationUnavailable) || readErr != nil || reader.State != "deleting" || p.readerDeletes != 1 || p.deletes != 0 {
		t.Fatalf("finished endpoint operation inferred absence: %+v %v %v", reader, err, readErr)
	}
	p.readerAbsent = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	reader, readErr = store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	if !errors.Is(err, errCloneCompensationUnavailable) || readErr != nil || reader.State != "retired" || p.readerDeletes != 1 || p.deletes != 1 {
		t.Fatalf("stored endpoint proof did not retire: %+v %v %v", reader, err, readErr)
	}
}

func TestPGClonePostgresSnapshotReaderCleanupUndispatchedReservationRetiresLocally(t *testing.T) {
	f, store, p, sourceID := cloneReaderWorkerFixture(t)
	if _, _, err := store.ReserveProjectEnvironmentClonePostgresCopyReader(t.Context(), f.lease, sourceID, 1); err != nil {
		t.Fatal(err)
	}
	cloneReaderCompensate(t, &f, store)
	readerIO := p.readerCreates + p.readerFinds + p.readerDeletes + p.readerReads
	plans, err := f.srv.capturedProjectEnvironmentDatabasePlans(t.Context(), f.lease.Operation)
	if err != nil {
		t.Fatal(err)
	}
	f.srv.managedPostgres = nil
	complete, err := f.srv.cleanupProjectEnvironmentClonePostgresCopyReader(t.Context(), f.lease, plans[0])
	reader, readErr := store.ProjectEnvironmentClonePostgresCopyReaderForLease(t.Context(), f.lease, sourceID)
	if err != nil || readErr != nil || !complete || reader.State != "retired" || !reader.RequestStartedAt.IsZero() || reader.EndpointID != "" || readerIO != p.readerCreates+p.readerFinds+p.readerDeletes+p.readerReads {
		t.Fatalf("local reader retirement used remote IO: %+v %v %v", reader, err, readErr)
	}
}
