//go:build !no_pg

// adr: 375
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneSnapshotFailureStore struct {
	*state.PgStore
	loseReservation, failRecord, loseRecord, loseCleanup bool
}

func (s *cloneSnapshotFailureStore) ReserveProjectEnvironmentClonePostgresSnapshot(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresSnapshot, error) {
	r, err := s.PgStore.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, l, id)
	if err == nil && s.loseReservation {
		s.loseReservation = false
		return state.ProjectEnvironmentClonePostgresSnapshot{}, errors.New("reservation reply lost")
	}
	return r, err
}
func (s *cloneSnapshotFailureStore) RecordProjectEnvironmentClonePostgresSnapshot(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresSnapshotObservation) (state.ProjectEnvironmentClonePostgresSnapshot, error) {
	if s.failRecord {
		s.failRecord = false
		return state.ProjectEnvironmentClonePostgresSnapshot{}, errors.New("receipt write unavailable")
	}
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresSnapshot(ctx, l, id, o)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return state.ProjectEnvironmentClonePostgresSnapshot{}, errors.New("receipt reply lost")
	}
	return r, err
}
func (s *cloneSnapshotFailureStore) FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresSnapshot, error) {
	r, err := s.PgStore.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, l, id)
	if err == nil && s.loseCleanup {
		s.loseCleanup = false
		return state.ProjectEnvironmentClonePostgresSnapshot{}, errors.New("cleanup reply lost")
	}
	return r, err
}

type cloneSnapshotProvider struct {
	environmentClonePostgresProvider
	actual                                                  managedpostgres.DatabaseSnapshot
	request                                                 managedpostgres.SnapshotCaptureRequest
	creates, finds, deletes                                 int
	loseCreation, pendingDelete                             bool
	deadlineMissing                                         bool
	forkActual                                              managedpostgres.SnapshotRestoreObservation
	forkRequest                                             managedpostgres.SnapshotRestoreRequest
	forkDefinition                                          managedpostgres.RestoreSourceDefinition
	forkCreates, forkFinds                                  int
	loseForkCreation, hideFork, forkPending                 bool
	beforeForkCreate                                        func(context.Context, managedpostgres.SnapshotRestoreRequest) error
	forkDeleteRequest                                       managedpostgres.SnapshotRestoreDeletionRequest
	forkDeleteIssued, forkDeleteReady                       bool
	loseForkDelete, emptyForkDeleteReply                    bool
	forkDeletes, forkDeleteObservations                     int
	beforeForkDelete                                        func(context.Context, managedpostgres.SnapshotRestoreDeletionRequest) error
	copyActual                                              managedpostgres.SnapshotCopyTargetObservation
	copyRequest                                             managedpostgres.SnapshotCopyTargetRequest
	copyDefinition                                          managedpostgres.RestoreSourceDefinition
	copyCreates, copyFinds                                  int
	loseCopyCreation, hideCopy, copyPending                 bool
	beforeCopyCreate                                        func(context.Context, managedpostgres.SnapshotCopyTargetRequest) error
	copyCleanupFinds, copyDeletes, copyDeletionObservations int
	copyDeletePending, copyDeleteIssued, loseCopyDelete     bool
	beforeCopyDelete                                        func(context.Context, managedpostgres.SnapshotCopyTargetRequest) error
}

func (p *cloneSnapshotProvider) CaptureSnapshot(ctx context.Context, r managedpostgres.SnapshotCaptureRequest) (managedpostgres.DatabaseSnapshot, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if p.actual.ProviderSnapshotID == "" {
		p.creates++
		p.request = r
		p.actual = managedpostgres.DatabaseSnapshot{ProviderSnapshotID: "source-project/snapshots/checkpoint", SourceResourceID: r.SourceResourceID,
			PointInTime: r.PointInTime, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	}
	if r != p.request {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	if p.loseCreation {
		p.loseCreation = false
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrUnavailable
	}
	return p.actual, nil
}
func (p *cloneSnapshotProvider) FindSnapshot(ctx context.Context, r managedpostgres.SnapshotCaptureRequest) (managedpostgres.DatabaseSnapshot, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	p.finds++
	if p.actual.ProviderSnapshotID == "" {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrNotFound
	}
	if r != p.request {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	return p.actual, nil
}
func (p *cloneSnapshotProvider) InspectSnapshot(context.Context, string) (managedpostgres.DatabaseSnapshot, error) {
	return p.actual, nil
}
func (p *cloneSnapshotProvider) DeleteSnapshot(ctx context.Context, r managedpostgres.SnapshotDeleteRequest) (managedpostgres.DeleteResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if r.ResourceID != p.request.ResourceID || r.SourceResourceID != p.request.SourceResourceID || !r.PointInTime.Equal(p.request.PointInTime) {
		return managedpostgres.DeleteResult{}, managedpostgres.ErrConflict
	}
	if p.actual.ProviderSnapshotID == "" {
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	p.deletes++
	if p.pendingDelete {
		return managedpostgres.DeleteResult{}, nil
	}
	p.actual = managedpostgres.DatabaseSnapshot{}
	return managedpostgres.DeleteResult{Done: true}, nil
}

func cloneSnapshotWorkerFixture(t *testing.T) (cloneCoordinatorFixture, *cloneSnapshotFailureStore, *cloneSnapshotProvider, string) {
	t.Helper()
	f := newCloneCoordinatorFixture(t, false)
	ctx := t.Context()
	if err := f.store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, time.Hour); err != nil {
		t.Fatal(err)
	}
	provider := &cloneSnapshotProvider{}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "snapshot-worker"}}},
		func(string) string { return "" }, map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return provider, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.managedPostgres = service
	account, project := f.lease.Operation.AccountID, f.lease.Operation.ProjectID
	source, err := service.Create(ctx, managedpostgres.CreateRequest{AccountID: account, Name: "snapshot-db", Spec: managedpostgres.Spec{Region: "eu", PostgresMajor: 17,
		Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	bindingID := uuid.NewString()
	if _, err := f.pool.Exec(ctx, `insert into managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access,provider_identity_id,credential_ref,state)
        values($1,$2,$3,$4,'production','DATABASE_URL','read_write','source-identity','source-credential','ready')`, bindingID, account, source.ID, f.apps[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PutManagedPostgresSecret(ctx, state.AppSecret{AccountID: account, AppID: f.apps[0].ID, Scope: "production", Key: "DATABASE_URL", Ciphertext: []byte("sealed-source"),
		Kid: "source-kid", ValueHash: strings.Repeat("a", 16), ManagedPostgresBindingID: bindingID, ManagedCredentialRef: "source-credential", ManagedCredentialGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	op, err := f.store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: account, ProjectID: project,
		SourceEnvironment: "production", TargetEnvironment: "snapshots", IdempotencyKey: "snapshots", SourceRevisionHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op, err = f.store.AdvanceProjectEnvironmentCloneOperation(ctx, account, project, op.ID, op.Status, state.CloneOperationCapturing, f.lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CaptureProjectEnvironmentCloneWorkloads(ctx, account, project, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	plans, err := f.srv.capturedProjectEnvironmentDatabasePlans(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := capturedProjectEnvironmentDatabaseResources(plans, time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	f.lease.Operation, err = f.store.AdvanceProjectEnvironmentCloneOperation(ctx, account, project, op.ID, op.Status, op.Status, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneSnapshotFailureStore{PgStore: f.store.PgStore}
	f.srv.store = store
	return f, store, provider, source.ID
}

func TestPGClonePostgresSnapshotWorkerRecoversReceipts(t *testing.T) {
	f, store, provider, sourceID := cloneSnapshotWorkerFixture(t)
	ctx := t.Context()
	store.loseReservation = true
	var err error
	f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
	if err == nil || provider.creates != 0 {
		t.Fatalf("lost intent reply reached provider: %v", err)
	}
	provider.loseCreation = true
	f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || provider.creates != 1 {
		t.Fatalf("lost creation reply = %v", err)
	}
	store.failRecord = true
	f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
	if err == nil || provider.creates != 1 {
		t.Fatalf("lost receipt write = %v", err)
	}
	store.loseRecord = true
	f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
	if err == nil || provider.creates != 1 {
		t.Fatalf("lost receipt reply = %v", err)
	}
	stale := f.lease
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker capture = %v", err)
	}
	if _, err := f.pool.Exec(ctx, "update managed_postgres_databases set restore_window_seconds=0 where id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
	if err != nil || provider.creates != 1 || provider.finds != 3 || provider.deadlineMissing {
		t.Fatalf("retained takeover = %v", err)
	}
	receipt, err := store.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, f.lease, sourceID)
	if err != nil || receipt.State != "retained" || receipt.ProviderSnapshotID != provider.actual.ProviderSnapshotID || !receipt.CapturePoint.Equal(provider.request.PointInTime) {
		t.Fatalf("receipt = %+v, %v", receipt, err)
	}
	expiry := time.Now().UTC().Add(time.Hour)
	provider.actual.ExpiresAt = &expiry
	if _, err := f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed retention replay = %v", err)
	}
	if f.lease.Operation.Status != state.CloneOperationCapturing || len(provider.restores) != 0 {
		t.Fatal("metadata snapshot advanced capture or restored target")
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, f.lease.Operation.AccountID, f.lease.Operation.ProjectID, "snapshots"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("snapshot receipt published target")
	}
}

func TestPGClonePostgresSnapshotCleanupRecoversUnknownCreation(t *testing.T) {
	for _, mode := range []string{"unknown_present", "unknown_absent", "pending", "lost_cleanup_reply"} {
		t.Run(mode, func(t *testing.T) {
			f, store, provider, sourceID := cloneSnapshotWorkerFixture(t)
			ctx := t.Context()
			var err error
			if mode == "unknown_absent" {
				_, err = store.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, f.lease, sourceID)
			} else {
				provider.loseCreation = true
				f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
				if errors.Is(err, managedpostgres.ErrUnavailable) {
					err = nil
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			op := f.lease.Operation
			f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
			if err != nil {
				t.Fatal(err)
			}
			provider.pendingDelete = mode == "pending"
			store.loseCleanup = mode == "lost_cleanup_reply"
			before := provider.creates
			var done bool
			f.lease, done, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
			if mode == "lost_cleanup_reply" {
				if err == nil {
					t.Fatal("lost cleanup reply hidden")
				}
				f.lease, done, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
			}
			if err != nil || done == (mode == "pending") || provider.creates != before || provider.deadlineMissing {
				t.Fatalf("cleanup done=%v error=%v", done, err)
			}
			receipt, err := store.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, f.lease, sourceID)
			want := "deleted"
			if mode == "pending" {
				want = "deleting"
			}
			if err != nil || receipt.State != want {
				t.Fatalf("cleanup receipt = %+v, %v", receipt, err)
			}
			if mode == "unknown_absent" && (provider.creates != 0 || provider.deletes != 0) {
				t.Fatal("unknown absent intent mutated provider")
			}
			if mode != "unknown_absent" && provider.deletes != 1 {
				t.Fatal("cleanup repeated deletion after acknowledgement recovery")
			}
		})
	}
}

func (p *cloneSnapshotProvider) RetainSnapshot(ctx context.Context, r managedpostgres.SnapshotCaptureRequest, id string) (managedpostgres.DatabaseSnapshot, error) {
	if id != p.actual.ProviderSnapshotID || r != p.request {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	p.actual.ExpiresAt = nil
	return p.actual, nil
}

func TestPGClonePostgresSnapshotUnknownDispatchDoesNotRecaptureOrReleaseHold(t *testing.T) {
	f, store, provider, sourceID := cloneSnapshotWorkerFixture(t)
	ctx := t.Context()
	if _, err := store.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, f.lease, sourceID); err != nil {
		t.Fatal(err)
	}
	receipt, dispatch, err := store.ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, f.lease, sourceID)
	if err != nil || !dispatch || receipt.RequestStartedAt.IsZero() {
		t.Fatalf("dispatch=%v err=%v", dispatch, err)
	}
	// Model a crash after dispatch commit, before a creation acknowledgement.
	// There is no evidence whether creation reached the provider.
	f.lease, err = f.srv.captureProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || provider.creates != 0 {
		t.Fatalf("unknown dispatch recaptured: %v", err)
	}
	op := f.lease.Operation
	f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	var done bool
	f.lease, done, err = f.srv.cleanupProjectEnvironmentClonePostgresSnapshots(ctx, f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || done || provider.creates != 0 || provider.deletes != 0 {
		t.Fatalf("unknown cleanup done=%v err=%v", done, err)
	}
	receipt, err = store.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, f.lease, sourceID)
	if err != nil || receipt.State != "deleting" || !receipt.CleanupObservedAt.IsZero() {
		t.Fatalf("unknown outcome hold = %+v, %v", receipt, err)
	}
	if _, err := store.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, f.lease, sourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("store released unknown request: %v", err)
	}
}
