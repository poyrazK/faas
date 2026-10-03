package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private bootstrap identity capture. Committed pins recover without SQL/provider
// IO; unreadable pins remain owned and can never be replaced by rediscovery.
func (s *server) projectEnvironmentClonePostgresTargetSQLPins(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan) (copyarchive.RestoreTarget, error) {
	pins, ok := s.store.(state.ProjectEnvironmentClonePostgresTargetSQLPinsStore)
	inventories, inventoryOK := s.store.(state.ProjectEnvironmentClonePostgresInventoryStore)
	if !ok || !inventoryOK || mfaIdentities == nil {
		return copyarchive.RestoreTarget{}, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	inventory, err := inventories.ProjectEnvironmentClonePostgresInventoryForLease(ctx, l, source.source.ID)
	if err != nil {
		return copyarchive.RestoreTarget{}, err
	}
	scope := inventory.Sealed.Scope
	d := clonePostgresSnapshotDefinition(source)
	if scope.SourceDatabaseID != source.source.ID || scope.SourceVersion != source.hash || scope.OperationID != l.Operation.ID || scope.AccountID != l.Operation.AccountID || scope.ProjectID != l.Operation.ProjectID ||
		scope.PostgresMajor != d.Spec.PostgresMajor || scope.BackendID != d.BackendID || scope.BackendFingerprint != d.BackendFingerprint || scope.SourceProviderResourceID != d.ProviderResourceID || scope.SourceDataResourceID != d.DataResourceID {
		return copyarchive.RestoreTarget{}, managedpostgres.ErrConflict
	}
	identities := mfaIdentities()
	receipt, err := pins.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx, l, source.source.ID)
	if err == nil {
		if receipt.InventoryFingerprint != inventory.Sealed.Fingerprint {
			return copyarchive.RestoreTarget{}, managedpostgres.ErrConflict
		}
		return copyarchive.OpenTarget(identities, scope, receipt.Sealed)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return copyarchive.RestoreTarget{}, err
	}
	if l.Operation.Status != state.CloneOperationCapturing || s.managedPostgres == nil || setSecretRecipient == nil {
		return copyarchive.RestoreTarget{}, managedpostgres.ErrUnavailable
	}
	recipient := setSecretRecipient()
	if !cloneInventoryCanOpen(recipient, identities) {
		return copyarchive.RestoreTarget{}, managedpostgres.ErrUnavailable
	}
	preparation, _, err := s.projectEnvironmentClonePostgresTargetSQLPreparation(ctx, l, source, scope)
	if err != nil {
		return copyarchive.RestoreTarget{}, err
	}
	actual, err := s.managedPostgres.InspectSnapshotCopyTargetSQL(ctx, d, preparation)
	if err != nil {
		return copyarchive.RestoreTarget{}, err
	}
	target := copyarchive.RestoreTarget{Scope: scope, OwnerID: preparation.ResourceID, ProviderResourceID: actual.ProviderResourceID, ProviderCreatedAt: actual.ProviderCreatedAt,
		DataResourceID: actual.DataResourceID, EndpointID: actual.EndpointID, EndpointCreatedAt: actual.EndpointCreatedAt, DatabaseName: actual.Identity.DatabaseName, DatabaseOID: actual.Identity.DatabaseOID, RoleName: actual.Identity.RoleName, RoleOID: actual.Identity.RoleOID}
	sealed, err := copyarchive.SealTarget(recipient, target)
	if err != nil {
		return copyarchive.RestoreTarget{}, err
	}
	receipt, _, err = pins.RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx, l, source.source.ID, inventory.Sealed.Fingerprint, sealed)
	if err != nil {
		return copyarchive.RestoreTarget{}, err
	}
	return copyarchive.OpenTarget(identities, scope, receipt.Sealed)
}

func (s *server) projectEnvironmentClonePostgresTargetSQLPreparation(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan,
	scope copyinventory.Scope) (managedpostgres.SnapshotCopyTargetRequest, state.ProjectEnvironmentClonePostgresCopyTarget, error) {
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	targets, targetOK := s.store.(state.ProjectEnvironmentClonePostgresCopyTargetStore)
	var zero managedpostgres.SnapshotCopyTargetRequest
	var target state.ProjectEnvironmentClonePostgresCopyTarget
	if !snapshotOK || !captureOK || !targetOK {
		return zero, target, managedpostgres.ErrUnavailable
	}
	snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, l, source.source.ID)
	if err == nil {
		err = validateClonePostgresSnapshotPlan(l.Operation, source, snapshot)
	}
	if err != nil {
		return zero, target, err
	}
	capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, l, source.source.ID)
	if err != nil {
		return zero, target, err
	}
	target, err = targets.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, l, source.source.ID)
	if err != nil {
		return zero, target, err
	}
	if snapshot.State != "retained" || snapshot.ProviderSnapshotID != scope.ProviderSnapshotID || !snapshot.CapturePoint.Equal(scope.CapturePoint) || !snapshot.SnapshotCreatedAt.Equal(scope.SnapshotCreatedAt) ||
		capture.State != "adopted" || capture.AdoptedDatabaseID != scope.CaptureDatabaseID || capture.TargetProviderResourceID != scope.CaptureProviderResourceID ||
		!capture.TargetCreatedAt.Equal(scope.CaptureCreatedAt) || target.State != "prepared" {
		return zero, target, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotCopyTargetRequest{ResourceID: target.TargetDatabaseID, ExpectedProviderResourceID: target.ProviderResourceID, ExpectedCreatedAt: target.ProviderCreatedAt,
		SnapshotCreatedAt: snapshot.SnapshotCreatedAt, CaptureCreatedAt: capture.TargetCreatedAt,
		Capture: managedpostgres.SnapshotRestoreRequest{ResourceID: capture.TargetOwnerID, ProviderSnapshotID: snapshot.ProviderSnapshotID, ExpectedTargetResourceID: capture.TargetProviderResourceID, Snapshot: clonePostgresSnapshotRequest(snapshot)}}, target, nil
}
