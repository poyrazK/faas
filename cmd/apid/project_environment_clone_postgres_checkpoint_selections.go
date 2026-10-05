package main

import (
	"context"
	"crypto/rand"
	"errors"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpointselection"
	"github.com/onebox-faas/faas/pkg/state"
)

// A qualified provider inventory must supply this exact source selection. This
// seam does not classify partial lists as complete database/writer coverage.
type clonePostgresCheckpointSelectionRead func(context.Context, checkpointselection.Scope) (managedpostgres.CheckpointConnectionRequest, error)

// Original committed metadata wins recovery. It is never replaced by current
// provider configuration, a new key or another database selection after handoff.
func (s *server) projectEnvironmentClonePostgresCheckpointSelection(ctx context.Context, l state.ProjectEnvironmentCloneLease, sourceID string, read clonePostgresCheckpointSelectionRead) (checkpointselection.Selection, error) {
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresCheckpointSelectionStore)
	if !ok || mfaIdentities == nil {
		return checkpointselection.Selection{}, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	scope, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, sourceID)
	if err != nil {
		return checkpointselection.Selection{}, err
	}
	identities := mfaIdentities()
	r, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(ctx, l, sourceID)
	if err == nil {
		return openCloneCheckpointSelection(ctx, identities, scope, r.Sealed)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return checkpointselection.Selection{}, err
	}
	if l.Operation.Status != state.CloneOperationCapturing || read == nil || setSecretRecipient == nil || s.cloneWorkerAdmission == nil {
		return checkpointselection.Selection{}, managedpostgres.ErrUnavailable
	}
	recipient := setSecretRecipient()
	if !cloneInventoryCanOpen(recipient, identities) {
		return checkpointselection.Selection{}, managedpostgres.ErrUnavailable
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return checkpointselection.Selection{}, err
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return checkpointselection.Selection{}, managedpostgres.ErrUnavailable
	}
	request, err := read(ctx, scope)
	if err != nil {
		return checkpointselection.Selection{}, err
	}
	if err := ctx.Err(); err != nil {
		return checkpointselection.Selection{}, err
	}
	sealed, err := checkpointselection.Seal(recipient, scope, request, key)
	if err != nil {
		return checkpointselection.Selection{}, err
	}
	r, _, err = store.RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx, l, sourceID, sealed)
	if err != nil {
		return checkpointselection.Selection{}, err
	}
	return openCloneCheckpointSelection(ctx, identities, scope, r.Sealed)
}

func openCloneCheckpointSelection(ctx context.Context, identities []*age.X25519Identity, scope checkpointselection.Scope, sealed checkpointselection.Sealed) (checkpointselection.Selection, error) {
	selection, err := checkpointselection.Open(identities, scope, sealed)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return checkpointselection.Selection{}, err
	}
	return selection, nil
}

// This private worker consumes only retained intent. It returns admission/session
// evidence, never a common point, source release or stage readiness. Unknown SQL
// outcomes retain the original control-plane source hold and selection.
func (s *server) closeProjectEnvironmentClonePostgresCheckpointConnections(ctx context.Context, l state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentDatabasePlan) (state.ProjectEnvironmentCloneLease, managedpostgres.CheckpointConnectionClosure, error) {
	var zero managedpostgres.CheckpointConnectionClosure
	if l.Operation.Status != state.CloneOperationCapturing {
		return l, zero, state.ErrConflict
	}
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresCheckpointSelectionStore)
	if !ok || s.managedPostgres == nil || mfaIdentities == nil || s.cloneWorkerAdmission == nil {
		return l, zero, managedpostgres.ErrUnavailable
	}
	stepCtx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	scope, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(stepCtx, l, plan.source.ID)
	if err == nil {
		err = validateCloneCheckpointSelectionPlan(scope, plan, l)
	}
	var receipt state.ProjectEnvironmentClonePostgresCheckpointSelection
	if err == nil {
		receipt, err = store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(stepCtx, l, plan.source.ID)
	}
	var selection checkpointselection.Selection
	if err == nil {
		selection, err = checkpointselection.Open(mfaIdentities(), scope, receipt.Sealed)
	}
	var request managedpostgres.CheckpointConnectionRequest
	if err == nil {
		request, err = selection.RequestForWorker(scope)
	}
	if err == nil {
		err = s.cloneWorkerAdmission(stepCtx)
	}
	cancel()
	if err != nil {
		return l, zero, err
	}
	var maintenance state.ProjectEnvironmentClonePostgresMaintenance
	l, maintenance, err = s.prepareProjectEnvironmentClonePostgresMaintenance(ctx, l, plan)
	if err != nil {
		return l, zero, err
	}
	if maintenance.ID != scope.MaintenanceID || maintenance.OwnerOID != scope.MaintenanceOwnerOID || maintenance.DatabaseOID != scope.MaintenanceDatabaseOID {
		return l, zero, state.ErrConflict
	}
	stepCtx, cancel = context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	if err := s.cloneWorkerAdmission(stepCtx); err != nil {
		return l, zero, err
	}
	definition, m := clonePostgresSnapshotDefinition(plan), clonePostgresMaintenanceObservation(maintenance)
	closed, err := s.managedPostgres.CloseCheckpointConnections(stepCtx, definition, m, request)
	if err != nil {
		return l, zero, err
	}
	if err := s.cloneWorkerAdmission(stepCtx); err != nil {
		return l, zero, err
	}
	actual, err := s.managedPostgres.ObserveCheckpointConnectionClosure(stepCtx, definition, m, request)
	if err != nil {
		return l, zero, err
	}
	if !sameCloneCheckpointConnectionPins(closed, actual) {
		return l, zero, state.ErrConflict
	}
	currentScope, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(stepCtx, l, plan.source.ID)
	if err != nil {
		return l, zero, err
	}
	current, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(stepCtx, l, plan.source.ID)
	if err != nil {
		return l, zero, err
	}
	if currentScope != scope || current.Sealed.Fingerprint != receipt.Sealed.Fingerprint || current.Sealed.KeyID != receipt.Sealed.KeyID || current.Sealed.CiphertextSHA256 != receipt.Sealed.CiphertextSHA256 {
		return l, zero, state.ErrConflict
	}
	if err := stepCtx.Err(); err != nil {
		return l, zero, err
	}
	return l, actual, nil
}

func validateCloneCheckpointSelectionPlan(scope checkpointselection.Scope, plan capturedProjectEnvironmentDatabasePlan, l state.ProjectEnvironmentCloneLease) error {
	d := clonePostgresSnapshotDefinition(plan)
	hash, err := state.ProjectEnvironmentCloneDatabaseSourceHash(state.ProjectEnvironmentClonePostgresBinding{
		DatabaseID: plan.source.ID, DatabaseName: plan.source.Name, BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint,
		ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Region: d.Spec.Region, PostgresMajor: d.Spec.PostgresMajor,
		ServiceClass: string(d.Spec.Class), Availability: string(d.Spec.Availability), ScaleToZero: d.Spec.ScaleToZero,
		StorageLimitBytes: d.Spec.StorageLimitBytes, RestoreWindowSeconds: d.Spec.RestoreWindowSeconds,
	})
	if err != nil || hash != scope.SourceVersion || plan.source.AccountID != scope.AccountID {
		return state.ErrConflict
	}
	if scope.OperationID != l.Operation.ID || scope.AccountID != l.Operation.AccountID || scope.ProjectID != l.Operation.ProjectID || scope.SourceDatabaseID != plan.source.ID || scope.SourceVersion != plan.hash ||
		scope.BackendID != d.BackendID || scope.BackendFingerprint != d.BackendFingerprint || scope.SourceProviderResourceID != d.ProviderResourceID || scope.SourceDataResourceID != d.DataResourceID || scope.PostgresMajor != d.Spec.PostgresMajor {
		return state.ErrConflict
	}
	return nil
}

func sameCloneCheckpointConnectionPins(a, b managedpostgres.CheckpointConnectionClosure) bool {
	if a.CheckpointConnectionIdentity != b.CheckpointConnectionIdentity || !a.ClosedAt.Equal(b.ClosedAt) || len(a.Databases) != len(b.Databases) {
		return false
	}
	for i, x := range a.Databases {
		y := b.Databases[i]
		if x.OID != y.OID || x.OwnerOID != y.OwnerOID || x.Name != y.Name || x.OriginalAllowConnections != y.OriginalAllowConnections {
			return false
		}
	}
	return true
}
