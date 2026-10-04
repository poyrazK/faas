//go:build !no_pg

// adr: 581
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

type cloneMaintenanceFailureStore struct {
	*state.PgStore
	loseReservation, loseFinish      bool
	failRecordPhase, loseRecordPhase string
}

func (s *cloneMaintenanceFailureStore) ReserveProjectEnvironmentClonePostgresMaintenance(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentClonePostgresMaintenance, error) {
	r, err := s.PgStore.ReserveProjectEnvironmentClonePostgresMaintenance(ctx, l, id)
	if err == nil && s.loseReservation {
		s.loseReservation = false
		return state.ProjectEnvironmentClonePostgresMaintenance{}, errors.New("maintenance reservation reply lost")
	}
	return r, err
}

func (s *cloneMaintenanceFailureStore) RecordProjectEnvironmentClonePostgresMaintenance(ctx context.Context, l state.ProjectEnvironmentCloneLease, id, phase string, o state.ProjectEnvironmentClonePostgresMaintenanceObservation) (state.ProjectEnvironmentClonePostgresMaintenance, error) {
	if s.failRecordPhase == phase {
		s.failRecordPhase = ""
		return state.ProjectEnvironmentClonePostgresMaintenance{}, errors.New("maintenance receipt unavailable")
	}
	r, err := s.PgStore.RecordProjectEnvironmentClonePostgresMaintenance(ctx, l, id, phase, o)
	if err == nil && s.loseRecordPhase == phase {
		s.loseRecordPhase = ""
		return state.ProjectEnvironmentClonePostgresMaintenance{}, errors.New("maintenance receipt reply lost")
	}
	return r, err
}

func (s *cloneMaintenanceFailureStore) FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentClonePostgresFenceAbandonment) (state.ProjectEnvironmentClonePostgresWriteFence, error) {
	r, err := s.PgStore.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, l, id, o)
	if err == nil && s.loseFinish {
		s.loseFinish = false
		return state.ProjectEnvironmentClonePostgresWriteFence{}, errors.New("source release reply lost")
	}
	return r, err
}

type cloneMaintenanceProvider struct {
	*cloneSnapshotProvider
	maintenance                                                   managedpostgres.CheckpointMaintenance
	terminal                                                      managedpostgres.CheckpointConnectionTerminal
	roles, databases, activations, readyReads, abandons, observes int
	losePhase                                                     string
	loseAbandon, missingObservation, substitutedObservation       bool
	onPhase                                                       func(managedpostgres.CheckpointMaintenanceRequest) error
	deadlineMissing                                               bool
}

func (p *cloneMaintenanceProvider) ReconcileCheckpointMaintenance(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, r managedpostgres.CheckpointMaintenanceRequest) (managedpostgres.CheckpointMaintenance, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if p.onPhase != nil {
		if err := p.onPhase(r); err != nil {
			return managedpostgres.CheckpointMaintenance{}, err
		}
	}
	if p.maintenance.OwnerToken != "" && (p.maintenance.OwnerToken != r.OwnerToken || p.maintenance.SourceResourceID != r.SourceResourceID) {
		return managedpostgres.CheckpointMaintenance{}, managedpostgres.ErrConflict
	}
	switch r.Phase {
	case "role":
		if p.maintenance.OwnerToken == "" {
			p.roles++
			p.maintenance = managedpostgres.CheckpointMaintenance{OwnerToken: r.OwnerToken, SourceResourceID: r.SourceResourceID, OwnerOID: 10101, State: "reserved"}
		}
	case "database":
		if p.maintenance.OwnerOID != r.OwnerOID {
			return managedpostgres.CheckpointMaintenance{}, managedpostgres.ErrConflict
		}
		if p.maintenance.DatabaseOID == 0 {
			p.databases++
			p.maintenance.DatabaseOID = 20202
		}
	case "activation":
		if p.maintenance.OwnerOID != r.OwnerOID || p.maintenance.DatabaseOID != r.DatabaseOID {
			return managedpostgres.CheckpointMaintenance{}, managedpostgres.ErrConflict
		}
		if p.maintenance.State != "ready" {
			p.activations++
			p.maintenance.State = "ready"
		}
	case "ready":
		p.readyReads++
		if p.maintenance.State != "ready" || p.maintenance.OwnerOID != r.OwnerOID || p.maintenance.DatabaseOID != r.DatabaseOID {
			return managedpostgres.CheckpointMaintenance{}, managedpostgres.ErrConflict
		}
	}
	if p.losePhase == r.Phase {
		p.losePhase = ""
		return managedpostgres.CheckpointMaintenance{}, managedpostgres.ErrUnavailable
	}
	return p.maintenance, nil
}

func (p *cloneMaintenanceProvider) AbandonCheckpointConnections(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, i managedpostgres.CheckpointConnectionIdentity) (managedpostgres.CheckpointConnectionTerminal, error) {
	p.abandons++
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if m != p.maintenance {
		return managedpostgres.CheckpointConnectionTerminal{}, managedpostgres.ErrConflict
	}
	if p.terminal.OwnerToken == "" {
		p.terminal = managedpostgres.CheckpointConnectionTerminal{CheckpointConnectionIdentity: i, State: "abandoned", ReleasedAt: time.Now().UTC().Truncate(time.Microsecond)}
	}
	if p.terminal.CheckpointConnectionIdentity != i {
		return managedpostgres.CheckpointConnectionTerminal{}, managedpostgres.ErrConflict
	}
	if p.loseAbandon {
		p.loseAbandon = false
		return managedpostgres.CheckpointConnectionTerminal{}, managedpostgres.ErrUnavailable
	}
	return p.terminal, nil
}

func (p *cloneMaintenanceProvider) ObserveCheckpointConnections(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, i managedpostgres.CheckpointConnectionIdentity) (managedpostgres.CheckpointConnectionTerminal, error) {
	p.observes++
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if p.missingObservation {
		return managedpostgres.CheckpointConnectionTerminal{}, managedpostgres.ErrNotFound
	}
	if m != p.maintenance || p.terminal.CheckpointConnectionIdentity != i {
		return managedpostgres.CheckpointConnectionTerminal{}, managedpostgres.ErrConflict
	}
	actual := p.terminal
	if p.substitutedObservation {
		actual.SourceResourceID += "-other"
	}
	return actual, nil
}

func cloneMaintenanceWorkerFixture(t *testing.T) (cloneCoordinatorFixture, *cloneMaintenanceFailureStore, *cloneMaintenanceProvider, capturedProjectEnvironmentDatabasePlan) {
	t.Helper()
	f, _, snapshots, id := cloneSnapshotWorkerFixture(t)
	store := &cloneMaintenanceFailureStore{PgStore: f.store.PgStore}
	provider := &cloneMaintenanceProvider{cloneSnapshotProvider: snapshots}
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
	f.srv.managedPostgres, err = managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.store = store
	plans, err := f.srv.capturedProjectEnvironmentDatabasePlans(t.Context(), f.lease.Operation)
	if err != nil || len(plans) != 1 || plans[0].source.ID != id {
		t.Fatalf("maintenance plan: %v", err)
	}
	if _, err := store.ReserveProjectEnvironmentClonePostgresWriteFence(t.Context(), f.lease, id); err != nil {
		t.Fatal(err)
	}
	return f, store, provider, plans[0]
}

func TestPGClonePostgresMaintenanceWorkerRecoversOwnedPhases(t *testing.T) {
	f, store, provider, plan := cloneMaintenanceWorkerFixture(t)
	ctx := t.Context()
	provider.onPhase = func(request managedpostgres.CheckpointMaintenanceRequest) error {
		r, err := store.ProjectEnvironmentClonePostgresMaintenanceForLease(ctx, f.lease, plan.source.ID)
		if err != nil {
			return err
		}
		if r.ID != request.OwnerToken || r.SourceDataResourceID != request.SourceResourceID || r.ID == f.lease.Operation.ID ||
			clonePostgresMaintenancePhase(r.State) != request.Phase || request.Phase != "ready" && r.State != request.Phase+"_requested" && r.State != "activation_requested" {
			return state.ErrConflict
		}
		return nil
	}
	store.loseReservation = true
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresMaintenance(ctx, f.lease, plan)
	if err == nil || provider.roles != 0 {
		t.Fatalf("lost reservation reached provider: %v", err)
	}
	provider.losePhase = "role"
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresMaintenance(ctx, f.lease, plan)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || provider.roles != 1 {
		t.Fatalf("lost role reply: %v", err)
	}
	stale := f.lease
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.srv.prepareProjectEnvironmentClonePostgresMaintenance(ctx, stale, plan); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker reached maintenance: %v", err)
	}
	store.failRecordPhase = "database"
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresMaintenance(ctx, f.lease, plan)
	if err == nil || provider.roles != 1 || provider.databases != 1 {
		t.Fatalf("lost database write: %v", err)
	}
	store.loseRecordPhase = "activation"
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresMaintenance(ctx, f.lease, plan)
	if err == nil || provider.activations != 1 {
		t.Fatalf("lost activation acknowledgement: %v", err)
	}
	var receipt state.ProjectEnvironmentClonePostgresMaintenance
	f.lease, receipt, err = f.srv.prepareProjectEnvironmentClonePostgresMaintenance(ctx, f.lease, plan)
	if err != nil || receipt.State != "ready" || provider.roles != 1 || provider.databases != 1 || provider.activations != 1 || provider.readyReads != 1 || provider.deadlineMissing {
		t.Fatalf("maintenance recovery: %+v %v", receipt, err)
	}
	if _, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID,
		state.CloneOperationCapturing, state.CloneOperationCopying, f.lease.Operation.Revision, f.lease.Operation.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ready maintenance advanced copying: %v", err)
	}
}

func TestPGClonePostgresFenceCoordinatorRequiresIndependentTerminalProof(t *testing.T) {
	f, store, provider, plan := cloneMaintenanceWorkerFixture(t)
	ctx := t.Context()
	var err error
	f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID,
		state.CloneOperationCapturing, state.CloneOperationCompensating, f.lease.Operation.Revision, f.lease.Operation.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	assertHeld := func() {
		t.Helper()
		rows, err := store.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, f.lease)
		if err != nil || len(rows) != 1 || rows[0].State != "abandoning" || !rows[0].ReleasedAt.IsZero() {
			t.Fatalf("unknown result released source: %+v %v", rows, err)
		}
	}
	provider.loseAbandon = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(ctx, store, f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || provider.observes != 0 {
		t.Fatalf("lost abandonment reply ignored: %v", err)
	}
	assertHeld()
	provider.missingObservation = true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(ctx, store, f.lease)
	if !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("missing terminal treated as proof: %v", err)
	}
	assertHeld()
	provider.missingObservation, provider.substitutedObservation = false, true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(ctx, store, f.lease)
	if !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("substituted terminal accepted: %v", err)
	}
	assertHeld()
	provider.substitutedObservation, store.loseFinish = false, true
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(ctx, store, f.lease)
	if err == nil {
		t.Fatal("lost source release acknowledgement was ignored")
	}
	stale := f.lease
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	before := provider.abandons
	if _, err := f.srv.abandonProjectEnvironmentClonePostgresWriteFences(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker resumed recovery: %v", err)
	}
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(ctx, store, f.lease)
	if !errors.Is(err, errCloneCompensationUnavailable) || f.lease.Operation.Status != state.CloneOperationCompensating || provider.abandons != before || provider.deadlineMissing {
		t.Fatalf("terminal recovery marked complete compensation: %v", err)
	}
	rows, err := store.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, f.lease)
	if err != nil || len(rows) != 1 || rows[0].State != "released" || rows[0].SourceDatabaseID != plan.source.ID || rows[0].RemoteTerminalState != "abandoned" {
		t.Fatalf("source terminal proof: %+v %v", rows, err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("abandonment published stage: %v", err)
	}
}
