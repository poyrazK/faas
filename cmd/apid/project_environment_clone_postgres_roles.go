package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/state"
)

type clonePostgresRoleInput struct {
	source    copyinventory.ExportPlan
	target    copyarchive.RestoreTarget
	inventory state.ProjectEnvironmentClonePostgresInventory
	pins      state.ProjectEnvironmentClonePostgresTargetSQLPins
}

// Recover the original source roles and bootstrap pins. Admission projection is
// unnecessary for role planning: this plan is never used to export databases.
// No source SQL, provider discovery or current encryption recipient is needed.
func (s *server) clonePostgresRoleInput(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan, identities []*age.X25519Identity) (clonePostgresRoleInput, error) {
	var input clonePostgresRoleInput
	inventories, inventoryOK := s.store.(state.ProjectEnvironmentClonePostgresInventoryStore)
	pins, pinsOK := s.store.(state.ProjectEnvironmentClonePostgresTargetSQLPinsStore)
	if !inventoryOK || !pinsOK {
		return input, managedpostgres.ErrUnavailable
	}
	var err error
	input.inventory, err = inventories.ProjectEnvironmentClonePostgresInventoryForLease(ctx, l, source.source.ID)
	if err != nil {
		return input, err
	}
	scope := input.inventory.Sealed.Scope
	if !clonePostgresInventoryMatchesSource(l, source, scope) {
		return input, managedpostgres.ErrConflict
	}
	inventory, err := copyinventory.OpenInventory(identities, scope, input.inventory.Sealed)
	if err != nil {
		return input, err
	}
	input.source, err = inventory.PlanExports(scope, nil)
	if err != nil {
		return input, err
	}
	input.pins, err = pins.ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx, l, source.source.ID)
	if err != nil {
		return input, err
	}
	if input.pins.InventoryFingerprint != input.inventory.Sealed.Fingerprint {
		return input, managedpostgres.ErrConflict
	}
	input.target, err = copyarchive.OpenTarget(identities, scope, input.pins.Sealed)
	return input, err
}

func (i clonePostgresRoleInput) open(identities []*age.X25519Identity, r state.ProjectEnvironmentClonePostgresRolePlan) (copyroles.Plan, error) {
	if r.TargetDatabaseID != i.target.OwnerID || r.InventoryCiphertextSHA256 != i.inventory.Sealed.CiphertextSHA256 || r.TargetPinsCiphertextSHA256 != i.pins.Sealed.CiphertextSHA256 {
		return copyroles.Plan{}, managedpostgres.ErrConflict
	}
	return copyroles.Open(identities, i.source, i.target, r.Sealed)
}

// Existing roles are pinned to the authenticated target catalogue OIDs. NewPlan
// additionally requires identical captured logical attributes/settings. Every
// source role is classified; differing provider roles fail before persistence.
func clonePostgresRoleDispositions(source copyinventory.ExportPlan, target copyinventory.Inventory) ([]copyroles.Disposition, error) {
	src, err := source.RoleCatalogueForWorker()
	if err != nil {
		return nil, err
	}
	base, err := target.RoleCatalogueForWorker()
	if err != nil {
		return nil, err
	}
	ids := make(map[string]uint32, len(base.Roles))
	for _, role := range base.Roles {
		ids[role.Name] = role.OID
	}
	dispositions := make([]copyroles.Disposition, 0, len(src.Roles))
	for _, role := range src.Roles {
		dispositions = append(dispositions, copyroles.Disposition{SourceOID: role.OID, ExistingTargetOID: ids[role.Name]})
	}
	return dispositions, nil
}

// Private capture only. A committed plan is recovered before any target SQL IO.
// Unreadable ownership and unknown commit replies never permit recapture/DDL.
func (s *server) projectEnvironmentClonePostgresRolePlan(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan) (copyroles.Plan, state.ProjectEnvironmentClonePostgresRolePlan, copyarchive.RestoreTarget, error) {
	var zero copyroles.Plan
	var receipt state.ProjectEnvironmentClonePostgresRolePlan
	var target copyarchive.RestoreTarget
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresRolePlanStore)
	if !ok || mfaIdentities == nil {
		return zero, receipt, target, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	identities := mfaIdentities()
	input, err := s.clonePostgresRoleInput(ctx, l, source, identities)
	if err != nil {
		return zero, receipt, target, err
	}
	receipt, err = store.ProjectEnvironmentClonePostgresRolePlanForLease(ctx, l, source.source.ID)
	if err == nil {
		plan, openErr := input.open(identities, receipt)
		return plan, receipt, input.target, openErr
	}
	if !errors.Is(err, state.ErrNotFound) {
		return zero, receipt, target, err
	}
	if l.Operation.Status != state.CloneOperationCapturing || s.managedPostgres == nil || setSecretRecipient == nil {
		return zero, receipt, target, managedpostgres.ErrUnavailable
	}
	recipient := setSecretRecipient()
	if !cloneInventoryCanOpen(recipient, identities) {
		return zero, receipt, target, managedpostgres.ErrUnavailable
	}
	if input.target.Scope.PostgresMajor < 16 {
		return zero, receipt, target, managedpostgres.ErrUnsupported
	}
	request, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, input.target.Scope, input.target)
	if err != nil {
		return zero, receipt, target, err
	}
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		return zero, receipt, target, managedpostgres.ErrUnavailable
	}
	var plan copyroles.Plan
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(sqlCtx context.Context, conn *pgx.Conn, id managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			inventory, err := copyinventory.Read(sqlCtx, conn, copyinventory.Config{PostgresMajor: id.PostgresMajor, DatabaseName: id.DatabaseName, DatabaseOID: id.DatabaseOID, RoleName: id.RoleName, RoleOID: id.RoleOID, FingerprintKey: key})
			if err != nil {
				return err
			}
			dispositions, err := clonePostgresRoleDispositions(input.source, inventory)
			if err == nil {
				plan, err = copyroles.NewPlan(input.source, inventory, input.target, dispositions)
			}
			return err
		})
	if err != nil {
		return zero, receipt, target, err
	}
	sealed, err := copyroles.Seal(recipient, plan)
	if err != nil {
		return zero, receipt, target, err
	}
	receipt, _, err = store.RecordProjectEnvironmentClonePostgresRolePlan(ctx, l, source.source.ID, sealed)
	if err != nil {
		return zero, state.ProjectEnvironmentClonePostgresRolePlan{}, target, err
	}
	plan, err = input.open(identities, receipt)
	return plan, receipt, input.target, err
}

// Private role seeding only. Target SQL commits roles and their identities in one
// transaction. Exact retries recover its journal with the original sealed plan;
// this produces no credential activation, database data or stage-ready receipt.
func (s *server) projectEnvironmentClonePostgresRoles(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan) (copyroles.Receipt, error) {
	var zero copyroles.Receipt
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	plan, owner, target, err := s.projectEnvironmentClonePostgresRolePlan(ctx, l, source)
	if err != nil {
		return zero, err
	}
	if s.managedPostgres == nil {
		return zero, managedpostgres.ErrUnavailable
	}
	request, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, target.Scope, target)
	if err != nil {
		return zero, err
	}
	store := s.store.(state.ProjectEnvironmentClonePostgresRolePlanStore)
	authorize := func(checkCtx context.Context, actual copyarchive.RestoreTarget) error {
		fresh, err := store.ProjectEnvironmentClonePostgresRolePlanForLease(checkCtx, l, source.source.ID)
		if err != nil {
			return err
		}
		if actual != target || !sameClonePostgresRolePlan(fresh, owner) {
			return managedpostgres.ErrConflict
		}
		return nil
	}
	if err := authorize(ctx, target); err != nil {
		return zero, err
	}
	var result copyroles.Receipt
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			var err error
			result, err = copyroles.Prepare(sqlCtx, conn, plan, authorize)
			return err
		})
	if err == nil {
		err = authorize(ctx, target)
	}
	if err != nil {
		return zero, err
	}
	return result, nil
}

func sameClonePostgresRolePlan(a, b state.ProjectEnvironmentClonePostgresRolePlan) bool {
	return a.TargetDatabaseID == b.TargetDatabaseID && a.InventoryCiphertextSHA256 == b.InventoryCiphertextSHA256 && a.TargetPinsCiphertextSHA256 == b.TargetPinsCiphertextSHA256 && a.CapturedAt.Equal(b.CapturedAt) &&
		a.Sealed.Scope.Equal(b.Sealed.Scope) && a.Sealed.InventoryFingerprint == b.Sealed.InventoryFingerprint && a.Sealed.TargetFingerprint == b.Sealed.TargetFingerprint && a.Sealed.KeyID == b.Sealed.KeyID &&
		a.Sealed.CiphertextSHA256 == b.Sealed.CiphertextSHA256 && bytes.Equal(a.Sealed.Ciphertext, b.Sealed.Ciphertext)
}
