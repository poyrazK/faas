//go:build !no_pg

// adr: 581
package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpointselection"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneCheckpointSelectionFailureStore struct {
	*cloneMaintenanceFailureStore
	loseRecord bool
}

func (s *cloneCheckpointSelectionFailureStore) RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, sealed checkpointselection.Sealed) (state.ProjectEnvironmentClonePostgresCheckpointSelection, bool, error) {
	r, created, err := s.PgStore.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, id, sealed)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return state.ProjectEnvironmentClonePostgresCheckpointSelection{}, false, managedpostgres.ErrUnavailable
	}
	return r, created, err
}

type cloneCheckpointClosureProvider struct {
	*cloneMaintenanceProvider
	closed                        managedpostgres.CheckpointConnectionClosure
	requests                      []managedpostgres.CheckpointConnectionRequest
	closes, closureReads          int
	loseClose, missingObservation bool
	drained                       bool
	changedPin                    string
	onObserve                     func(context.Context) error
}

func (p *cloneCheckpointClosureProvider) CloseCheckpointConnections(ctx context.Context, d managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, r managedpostgres.CheckpointConnectionRequest) (managedpostgres.CheckpointConnectionClosure, error) {
	p.closes++
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if m != p.maintenance || r.SourceResourceID != d.DataResourceID {
		return managedpostgres.CheckpointConnectionClosure{}, managedpostgres.ErrConflict
	}
	retained := r
	retained.DatabaseNames = slices.Clone(r.DatabaseNames)
	p.requests = append(p.requests, retained)
	if p.closed.OwnerToken == "" {
		p.closed = managedpostgres.CheckpointConnectionClosure{CheckpointConnectionIdentity: r.CheckpointConnectionIdentity, State: "closed", ClosedAt: time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)}
		for i, name := range r.DatabaseNames {
			p.closed.Databases = append(p.closed.Databases, managedpostgres.CheckpointConnectionDatabase{Name: name, OID: uint32(30303 + i), OwnerOID: 40404, OriginalAllowConnections: true, Sessions: 1})
		}
	}
	if p.loseClose {
		p.loseClose = false
		return managedpostgres.CheckpointConnectionClosure{}, managedpostgres.ErrUnavailable
	}
	return p.closed, nil
}

func (p *cloneCheckpointClosureProvider) ObserveCheckpointConnectionClosure(ctx context.Context, d managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, r managedpostgres.CheckpointConnectionRequest) (managedpostgres.CheckpointConnectionClosure, error) {
	p.closureReads++
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if p.onObserve != nil {
		if err := p.onObserve(ctx); err != nil {
			return managedpostgres.CheckpointConnectionClosure{}, err
		}
	}
	if p.missingObservation {
		return managedpostgres.CheckpointConnectionClosure{}, managedpostgres.ErrNotFound
	}
	if m != p.maintenance || r.SourceResourceID != d.DataResourceID || p.closed.CheckpointConnectionIdentity != r.CheckpointConnectionIdentity {
		return managedpostgres.CheckpointConnectionClosure{}, managedpostgres.ErrConflict
	}
	actual := p.closed
	actual.Databases = slices.Clone(p.closed.Databases)
	actual.Drained = p.drained
	if p.drained {
		for i := range actual.Databases {
			actual.Databases[i].Sessions = 0
		}
	}
	switch p.changedPin {
	case "oid":
		actual.Databases[0].OID += 1000
	case "owner":
		actual.Databases[0].OwnerOID++
	case "allow":
		actual.Databases[0].OriginalAllowConnections = false
	case "time":
		actual.ClosedAt = actual.ClosedAt.Add(time.Microsecond)
	}
	return actual, nil
}

func cloneCheckpointSelectionWorkerFixture(t *testing.T) (cloneCoordinatorFixture, *cloneCheckpointSelectionFailureStore, *cloneCheckpointClosureProvider, capturedProjectEnvironmentDatabasePlan, *age.X25519Identity, clonePostgresCheckpointSelectionRead, *int) {
	t.Helper()
	f, maintenanceStore, maintenanceProvider, plan := cloneMaintenanceWorkerFixture(t)
	// The shared snapshot fixture supplies a synthetic point. Remove that test
	// input so this contract exercises original intent before any point exists.
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_operations set resources='[]'::jsonb where id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	f.lease.Operation.Resources = nil
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresMaintenance(t.Context(), f.lease, plan)
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneCheckpointSelectionFailureStore{cloneMaintenanceFailureStore: maintenanceStore}
	provider := &cloneCheckpointClosureProvider{cloneMaintenanceProvider: maintenanceProvider}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "snapshot-worker"}}}, func(string) string { return "" },
		map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return provider, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	f.srv.managedPostgres, err = managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.store = store
	f.srv.cloneWorkerAdmission = func(context.Context) error { return nil }
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipient, oldIdentities := setSecretRecipient, mfaIdentities
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { setSecretRecipient, mfaIdentities = oldRecipient, oldIdentities })
	reads := new(int)
	read := func(ctx context.Context, scope checkpointselection.Scope) (managedpostgres.CheckpointConnectionRequest, error) {
		(*reads)++
		if _, ok := ctx.Deadline(); !ok || scope.SourceDatabaseID != plan.source.ID {
			return managedpostgres.CheckpointConnectionRequest{}, managedpostgres.ErrConflict
		}
		// Synthetic provider selection plus real control-plane persistence; this
		// fixture does not attest native SQL closure or complete provider coverage.
		return managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{OwnerToken: scope.OperationID, SourceResourceID: scope.SourceDataResourceID}, DatabaseNames: []string{"source_private_beta", "source_private_alpha"}}, nil
	}
	return f, store, provider, plan, identity, read, reads
}

func assertCloneCheckpointHold(t *testing.T, f cloneCoordinatorFixture, store *cloneCheckpointSelectionFailureStore) {
	t.Helper()
	fences, err := store.ProjectEnvironmentClonePostgresWriteFencesForLease(t.Context(), f.lease)
	if err != nil || len(fences) != 1 || fences[0].State != "held" || !fences[0].ReleasedAt.IsZero() {
		t.Fatalf("closure lost recovery authority: %+v %v", fences, err)
	}
	var status string
	var resources, targets int
	if err := f.pool.QueryRow(t.Context(), "select status,jsonb_array_length(resources) from project_environment_clone_operations where id=$1", f.lease.Operation.ID).Scan(&status, &resources); err != nil || status != state.CloneOperationCapturing || resources != 0 {
		t.Fatalf("closure supplied checkpoint/publication: %s %d %v", status, resources, err)
	}
	if err := f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", f.lease.Operation.AccountID).Scan(&targets); err != nil || targets != 1 {
		t.Fatalf("closure reserved target data: %d %v", targets, err)
	}
}

func TestPGClonePostgresCheckpointSelectionWorkerRecoversOriginalAfterHandoffAndRotation(t *testing.T) {
	f, store, _, plan, previous, read, reads := cloneCheckpointSelectionWorkerFixture(t)
	store.loseRecord = true
	if actual, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, read); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(actual, checkpointselection.Selection{}) || *reads != 1 {
		t.Fatalf("lost committed selection: reads=%d err=%v", *reads, err)
	}
	original, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(t.Context(), f.lease, plan.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := f.lease
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return current.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, previous} }
	if _, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), stale, plan.source.ID, read); !errors.Is(err, state.ErrConflict) || *reads != 1 {
		t.Fatalf("stale worker recaptured: %v", err)
	}
	selection, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, nil)
	if err != nil || *reads != 1 {
		t.Fatalf("handoff recaptured: reads=%d err=%v", *reads, err)
	}
	request, err := selection.RequestForWorker(original.Sealed.Scope)
	if err != nil || !slices.Equal(request.DatabaseNames, []string{"source_private_alpha", "source_private_beta"}) || request.OwnerToken != f.lease.Operation.ID {
		t.Fatalf("original selection changed: %v", err)
	}
	recovered, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(t.Context(), f.lease, plan.source.ID)
	if err != nil || !bytes.Equal(original.Sealed.Ciphertext, recovered.Sealed.Ciphertext) || original.Sealed.Fingerprint != recovered.Sealed.Fingerprint || !original.RetainedAt.Equal(recovered.RetainedAt) {
		t.Fatalf("rotation replaced original intent: %v", err)
	}
	assertCloneCheckpointHold(t, f, store)
}

func TestPGClonePostgresCheckpointSelectionWorkerRefusesNewReadsAndUnreadableOriginal(t *testing.T) {
	f, store, provider, plan, identity, read, reads := cloneCheckpointSelectionWorkerFixture(t)
	for _, admission := range []func(context.Context) error{nil, func(context.Context) error { return managedpostgres.ErrUnavailable }} {
		f.srv.cloneWorkerAdmission = admission
		if _, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, read); !errors.Is(err, managedpostgres.ErrUnavailable) || *reads != 0 {
			t.Fatalf("unadmitted selection reached source: %v", err)
		}
	}
	f.srv.cloneWorkerAdmission = func(context.Context) error { return nil }
	if _, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, read); err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{other} }
	if _, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, read); !errors.Is(err, managedpostgres.ErrUnavailable) || *reads != 1 {
		t.Fatalf("missing original key recaptured: %v", err)
	}
	before := provider.readyReads
	if _, actual, err := f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, plan); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) || provider.closes != 0 || provider.readyReads != before {
		t.Fatalf("missing key reached source closure: %v", err)
	}
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_postgres_checkpoint_selections set ciphertext=set_byte(ciphertext,octet_length(ciphertext)-1,get_byte(ciphertext,octet_length(ciphertext)-1)#1) where operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, read); !errors.Is(err, state.ErrConflict) || *reads != 1 {
		t.Fatalf("damaged original recaptured: %v", err)
	}
	if _, actual, err := f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, plan); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) || provider.closes != 0 || provider.readyReads != before {
		t.Fatalf("damaged original reached closure: %v", err)
	}
	assertCloneCheckpointHold(t, f, store)
}

func TestPGClonePostgresCheckpointConnectionsRequireRetainedSelectionFrozenPlanAndAdmission(t *testing.T) {
	f, store, provider, plan, _, read, _ := cloneCheckpointSelectionWorkerFixture(t)
	before := provider.readyReads
	if _, actual, err := f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, plan); !errors.Is(err, state.ErrNotFound) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) || provider.closes != 0 || provider.readyReads != before {
		t.Fatalf("missing selection reached closure: %v", err)
	}
	if _, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, read); err != nil {
		t.Fatal(err)
	}
	changed := plan
	changed.source.Spec.StorageLimitBytes++
	if _, actual, err := f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, changed); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) || provider.closes != 0 || provider.readyReads != before {
		t.Fatalf("forged captured spec reached closure: %v", err)
	}
	for _, admission := range []func(context.Context) error{nil, func(context.Context) error { return managedpostgres.ErrUnavailable }} {
		f.srv.cloneWorkerAdmission = admission
		if _, actual, err := f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, plan); !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) || provider.closes != 0 || provider.readyReads != before {
			t.Fatalf("unadmitted closure reached source: %v", err)
		}
	}
	assertCloneCheckpointHold(t, f, store)
}

func TestPGClonePostgresCheckpointConnectionsRecoverOriginalAndRequireIndependentPins(t *testing.T) {
	f, store, provider, plan, _, read, reads := cloneCheckpointSelectionWorkerFixture(t)
	if _, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, read); err != nil {
		t.Fatal(err)
	}
	provider.loseClose = true
	var actual managedpostgres.CheckpointConnectionClosure
	var err error
	f.lease, actual, err = f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, plan)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) || provider.closes != 1 || provider.closureReads != 0 {
		t.Fatalf("lost close reply supplied proof: %v", err)
	}
	assertCloneCheckpointHold(t, f, store)
	provider.missingObservation = true
	f.lease, actual, err = f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, plan)
	if !errors.Is(err, managedpostgres.ErrNotFound) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) {
		t.Fatalf("missing independent observation supplied proof: %v", err)
	}
	provider.missingObservation = false
	for _, pin := range []string{"oid", "owner", "allow", "time"} {
		provider.changedPin = pin
		f.lease, actual, err = f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, plan)
		if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) {
			t.Fatalf("changed %s supplied proof: %v", pin, err)
		}
	}
	provider.changedPin, provider.drained = "", true
	f.lease, actual, err = f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(t.Context(), f.lease, plan)
	if err != nil || !actual.Drained || !actual.ClosedAt.Equal(provider.closed.ClosedAt) || *reads != 1 || provider.deadlineMissing || provider.roles != 1 || provider.databases != 1 || provider.activations != 1 {
		t.Fatalf("original closure/drain recovery: %v", err)
	}
	for _, r := range provider.requests {
		if r.OwnerToken != f.lease.Operation.ID || r.OwnerToken == provider.maintenance.OwnerToken || !slices.Equal(r.DatabaseNames, []string{"source_private_alpha", "source_private_beta"}) {
			t.Fatal("retry changed selected databases or barrier owner")
		}
	}
	assertCloneCheckpointHold(t, f, store)
}

func TestPGClonePostgresCheckpointConnectionsRejectPostClosureCancellationAndLeaseHandoff(t *testing.T) {
	for _, fault := range []string{"cancel", "handoff", "admission"} {
		t.Run(fault, func(t *testing.T) {
			f, store, provider, plan, _, read, _ := cloneCheckpointSelectionWorkerFixture(t)
			if _, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan.source.ID, read); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if fault == "admission" {
				admissions := 0
				f.srv.cloneWorkerAdmission = func(context.Context) error {
					admissions++
					if admissions == 3 {
						return managedpostgres.ErrUnavailable
					}
					return nil
				}
			}
			provider.onObserve = func(observeCtx context.Context) error {
				if fault == "cancel" {
					cancel()
					return nil
				}
				if err := store.ReleaseProjectEnvironmentCloneLease(observeCtx, f.lease, 0); err != nil {
					return err
				}
				var err error
				f.lease, err = store.ClaimNextProjectEnvironmentClone(observeCtx, uuid.NewString(), time.Minute)
				return err
			}
			l, actual, err := f.srv.closeProjectEnvironmentClonePostgresCheckpointConnections(ctx, f.lease, plan)
			want := state.ErrConflict
			if fault == "cancel" {
				want = context.Canceled
				f.lease = l
			} else if fault == "admission" {
				want = managedpostgres.ErrUnavailable
				f.lease = l
			}
			observations := 1
			if fault == "admission" {
				observations = 0
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(actual, managedpostgres.CheckpointConnectionClosure{}) || provider.closes != 1 || provider.closureReads != observations || provider.closed.OwnerToken == "" {
				t.Fatalf("post-commit %s returned closure authority: %v", fault, err)
			}
			assertCloneCheckpointHold(t, f, store)
			if _, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(t.Context(), f.lease, plan.source.ID); err != nil {
				t.Fatalf("unknown result lost original selection: %v", err)
			}
		})
	}
}

func TestPGClonePostgresCheckpointSelectionWorkerRefusesCancelledReadBeforeRetention(t *testing.T) {
	f, store, _, plan, _, read, reads := cloneCheckpointSelectionWorkerFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cancelRead := func(ctx context.Context, scope checkpointselection.Scope) (managedpostgres.CheckpointConnectionRequest, error) {
		request, err := read(ctx, scope)
		cancel()
		return request, err
	}
	actual, err := f.srv.projectEnvironmentClonePostgresCheckpointSelection(ctx, f.lease, plan.source.ID, cancelRead)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(actual, checkpointselection.Selection{}) || *reads != 1 {
		t.Fatalf("cancelled source read supplied selection: %v", err)
	}
	if _, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(t.Context(), f.lease, plan.source.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cancelled source read retained intent: %v", err)
	}
	assertCloneCheckpointHold(t, f, store)
}
