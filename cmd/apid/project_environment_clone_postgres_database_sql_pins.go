package main

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Retain one original preparation receipt for an already charged source archive.
// Retried metadata recovery requires no SQL/current recipient. The receipt still
// needs read-only target verification and qualified maintenance before import.
func (s *server) projectEnvironmentClonePostgresDatabaseSQLPins(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan, oid uint32) (copydatabases.Receipt, state.ProjectEnvironmentClonePostgresDatabaseSQLPins, error) {
	var zero copydatabases.Receipt
	var owner state.ProjectEnvironmentClonePostgresDatabaseSQLPins
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresDatabaseSQLPinsStore)
	archives, archiveOK := s.store.(state.ProjectEnvironmentClonePostgresArchiveStore)
	if !ok || !archiveOK || mfaIdentities == nil {
		return zero, owner, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	// The selected source OID must belong to this original complete plan. A
	// charged but unrelated archive reservation never supplies database intent.
	requirements, err := exports.RequirementsForWorker()
	if err != nil {
		return zero, owner, err
	}
	var selected copyinventory.DatabaseExport
	for _, d := range requirements {
		if d.Database.OID == oid {
			selected = d
		}
	}
	if selected.Database.OID == 0 || !clonePostgresInventoryMatchesSource(l, source, selected.Scope) {
		return zero, owner, managedpostgres.ErrConflict
	}
	a, err := archives.ProjectEnvironmentClonePostgresArchiveForLease(ctx, l, source.source.ID, oid)
	if err != nil {
		return zero, owner, err
	}
	if !a.Scope.Equal(selected.Scope) || a.InventoryFingerprint != selected.InventoryFingerprint {
		return zero, owner, managedpostgres.ErrConflict
	}
	plan, parent, bootstrap, err := s.projectEnvironmentClonePostgresDatabasePlan(ctx, l, source, exports)
	if err != nil {
		return zero, owner, err
	}
	owner, err = store.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(ctx, l, source.source.ID, oid)
	open := func(owned state.ProjectEnvironmentClonePostgresDatabaseSQLPins) (copydatabases.Receipt, error) {
		if owned.DatabasePlanCiphertextSHA256 != parent.Sealed.CiphertextSHA256 || owned.ArchiveOwnerID != a.OwnerID || owned.ArchiveReservationSHA256 != a.ReservationFingerprint() {
			return zero, managedpostgres.ErrConflict
		}
		return copydatabases.OpenPreparation(mfaIdentities(), exports, plan, oid, owned.Sealed)
	}
	if err == nil {
		r, e := open(owner)
		return r, owner, e
	}
	if !errors.Is(err, state.ErrNotFound) {
		return zero, owner, err
	}
	if l.Operation.Status != state.CloneOperationCapturing || s.managedPostgres == nil || setSecretRecipient == nil {
		return zero, owner, managedpostgres.ErrUnavailable
	}
	recipient := setSecretRecipient()
	if !cloneInventoryCanOpen(recipient, mfaIdentities()) {
		return zero, owner, managedpostgres.ErrUnavailable
	}
	parents := s.store.(state.ProjectEnvironmentClonePostgresDatabasePlanStore)
	authorize := func(checkCtx context.Context, actual copyarchive.RestoreTarget) error {
		fresh, err := parents.ProjectEnvironmentClonePostgresDatabasePlanForLease(checkCtx, l, source.source.ID)
		if err != nil {
			return err
		}
		current, err := archives.ProjectEnvironmentClonePostgresArchiveForLease(checkCtx, l, source.source.ID, oid)
		if err == nil && (actual != bootstrap || !sameClonePostgresDatabasePlan(fresh, parent) || current.ReservationFingerprint() != a.ReservationFingerprint()) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	if err = authorize(ctx, bootstrap); err != nil {
		return zero, owner, err
	}
	request, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, bootstrap.Scope, bootstrap)
	if err != nil {
		return zero, owner, err
	}
	var prepared copydatabases.Receipt
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request, func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
		var err error
		prepared, err = copydatabases.Prepare(sqlCtx, conn, exports, plan, oid, authorize)
		return err
	})
	if err != nil {
		return zero, owner, err
	}
	sealed, err := copydatabases.SealPreparation(recipient, prepared)
	if err != nil {
		return zero, owner, err
	}
	owner, _, err = store.RecordProjectEnvironmentClonePostgresDatabaseSQLPins(ctx, l, source.source.ID, oid, parent.Sealed.CiphertextSHA256, a.ReservationFingerprint(), sealed)
	if err != nil {
		return zero, state.ProjectEnvironmentClonePostgresDatabaseSQLPins{}, err
	}
	r, err := open(owner)
	return r, owner, err
}

// Authenticate a recovered child against the original bootstrap SQL journal,
// exact completion time, live durable child ownership and provider postchecks.
// This read-only seam neither opens closed databases nor dispatches imports.
func (s *server) projectEnvironmentClonePostgresDatabasePreparationForSQL(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan, oid uint32) (copydatabases.Receipt, error) {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresDatabaseSQLPinsStore)
	parents, parentOK := s.store.(state.ProjectEnvironmentClonePostgresDatabasePlanStore)
	roles, roleOK := s.store.(state.ProjectEnvironmentClonePostgresRolePlanStore)
	if !ok || !parentOK || !roleOK || mfaIdentities == nil {
		return copydatabases.Receipt{}, managedpostgres.ErrUnavailable
	}
	// Verification requires existing durable ownership. Calling the capture
	// helper here could create an absent parent/database instead of rejecting it.
	owner, err := store.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(ctx, l, source.source.ID, oid)
	if err != nil {
		return copydatabases.Receipt{}, err
	}
	parent, err := parents.ProjectEnvironmentClonePostgresDatabasePlanForLease(ctx, l, source.source.ID)
	if err != nil || owner.DatabasePlanCiphertextSHA256 != parent.Sealed.CiphertextSHA256 {
		if err == nil {
			err = managedpostgres.ErrConflict
		}
		return copydatabases.Receipt{}, err
	}
	role, err := roles.ProjectEnvironmentClonePostgresRolePlanForLease(ctx, l, source.source.ID)
	if err != nil {
		return copydatabases.Receipt{}, err
	}
	ids := mfaIdentities()
	input, err := s.clonePostgresDatabaseInput(ctx, l, source, ids, exports)
	if err != nil {
		return copydatabases.Receipt{}, err
	}
	plan, err := input.openDatabases(ids, role, parent)
	if err != nil {
		return copydatabases.Receipt{}, err
	}
	r, err := copydatabases.OpenPreparation(ids, exports, plan, oid, owner.Sealed)
	if err != nil {
		return copydatabases.Receipt{}, err
	}
	if s.managedPostgres == nil {
		return copydatabases.Receipt{}, managedpostgres.ErrUnavailable
	}
	bootstrap := input.target
	authorize := func(checkCtx context.Context, actual copyarchive.RestoreTarget) error {
		fresh, err := store.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(checkCtx, l, source.source.ID, oid)
		if err == nil && (actual != bootstrap || !sameClonePostgresDatabaseSQLPins(fresh, owner)) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	if err = authorize(ctx, bootstrap); err != nil {
		return copydatabases.Receipt{}, err
	}
	request, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, bootstrap.Scope, bootstrap)
	if err != nil {
		return copydatabases.Receipt{}, err
	}
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request, func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
		return r.VerifyForWorker(sqlCtx, conn, exports, authorize)
	})
	if err == nil {
		err = authorize(ctx, bootstrap)
	}
	if err != nil {
		return copydatabases.Receipt{}, err
	}
	return r, nil
}

func sameClonePostgresDatabaseSQLPins(a, b state.ProjectEnvironmentClonePostgresDatabaseSQLPins) bool {
	return a.DatabasePlanCiphertextSHA256 == b.DatabasePlanCiphertextSHA256 && a.ArchiveReservationSHA256 == b.ArchiveReservationSHA256 && a.ArchiveOwnerID == b.ArchiveOwnerID && a.CapturedAt.Equal(b.CapturedAt) &&
		a.Sealed.Scope.Equal(b.Sealed.Scope) && a.Sealed.OwnerID == b.Sealed.OwnerID && a.Sealed.ProviderResourceID == b.Sealed.ProviderResourceID && a.Sealed.ProviderCreatedAt.Equal(b.Sealed.ProviderCreatedAt) && a.Sealed.Fingerprint == b.Sealed.Fingerprint && a.Sealed.KeyID == b.Sealed.KeyID && a.Sealed.CiphertextSHA256 == b.Sealed.CiphertextSHA256 && bytes.Equal(a.Sealed.Ciphertext, b.Sealed.Ciphertext)
}
