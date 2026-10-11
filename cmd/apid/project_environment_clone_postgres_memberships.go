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

func (i clonePostgresRoleInput) openMemberships(identities []*age.X25519Identity, roles state.ProjectEnvironmentClonePostgresRolePlan, r state.ProjectEnvironmentClonePostgresMembershipPlan) (copyroles.MembershipPlan, error) {
	if r.TargetDatabaseID != i.target.OwnerID || r.InventoryCiphertextSHA256 != i.inventory.Sealed.CiphertextSHA256 || r.TargetPinsCiphertextSHA256 != i.pins.Sealed.CiphertextSHA256 || r.RolePlanCiphertextSHA256 != roles.Sealed.CiphertextSHA256 {
		return copyroles.MembershipPlan{}, managedpostgres.ErrConflict
	}
	plan, err := i.open(identities, roles)
	if err != nil {
		return copyroles.MembershipPlan{}, err
	}
	return copyroles.RecoverMemberships(identities, i.source, plan, r.Sealed)
}

// Retain the complete graph after role seeding. Recovery uses original encrypted
// metadata before SQL IO; target drift or an unknown record reply cannot rebase
// the original role identities, grantors, options or target membership baseline.
func (s *server) projectEnvironmentClonePostgresMembershipPlan(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan) (copyroles.MembershipPlan, state.ProjectEnvironmentClonePostgresMembershipPlan, copyarchive.RestoreTarget, error) {
	var zero copyroles.MembershipPlan
	var receipt state.ProjectEnvironmentClonePostgresMembershipPlan
	var target copyarchive.RestoreTarget
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresMembershipPlanStore)
	if !ok || mfaIdentities == nil {
		return zero, receipt, target, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	_, roleOwner, target, err := s.projectEnvironmentClonePostgresRolePlan(ctx, l, source)
	if err != nil {
		return zero, receipt, target, err
	}
	identities := mfaIdentities()
	input, err := s.clonePostgresRoleInput(ctx, l, source, identities)
	if err != nil || input.target != target {
		if err == nil {
			err = managedpostgres.ErrConflict
		}
		return zero, receipt, target, err
	}
	receipt, err = store.ProjectEnvironmentClonePostgresMembershipPlanForLease(ctx, l, source.source.ID)
	if err == nil {
		plan, openErr := input.openMemberships(identities, roleOwner, receipt)
		return plan, receipt, target, openErr
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
	seed, err := s.projectEnvironmentClonePostgresRoles(ctx, l, source)
	if err != nil {
		return zero, receipt, target, err
	}
	roleStore := s.store.(state.ProjectEnvironmentClonePostgresRolePlanStore)
	authorize := func(checkCtx context.Context) error {
		fresh, err := roleStore.ProjectEnvironmentClonePostgresRolePlanForLease(checkCtx, l, source.source.ID)
		if err == nil && !sameClonePostgresRolePlan(fresh, roleOwner) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	if err := authorize(ctx); err != nil {
		return zero, receipt, target, err
	}
	request, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, target.Scope, target)
	if err != nil {
		return zero, receipt, target, err
	}
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		return zero, receipt, target, managedpostgres.ErrUnavailable
	}
	var plan copyroles.MembershipPlan
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(sqlCtx context.Context, conn *pgx.Conn, id managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			if err := authorize(sqlCtx); err != nil {
				return err
			}
			inventory, err := copyinventory.Read(sqlCtx, conn, copyinventory.Config{PostgresMajor: id.PostgresMajor, DatabaseName: id.DatabaseName, DatabaseOID: id.DatabaseOID, RoleName: id.RoleName, RoleOID: id.RoleOID, FingerprintKey: key})
			if err == nil {
				plan, err = copyroles.NewMembershipPlan(input.source, seed, inventory)
			}
			return err
		})
	if err != nil {
		return zero, receipt, target, err
	}
	sealed, err := copyroles.SealMemberships(recipient, plan)
	if err != nil {
		return zero, receipt, target, err
	}
	receipt, _, err = store.RecordProjectEnvironmentClonePostgresMembershipPlan(ctx, l, source.source.ID, roleOwner.Sealed.CiphertextSHA256, sealed)
	if err != nil {
		return zero, state.ProjectEnvironmentClonePostgresMembershipPlan{}, target, err
	}
	plan, err = input.openMemberships(identities, roleOwner, receipt)
	return plan, receipt, target, err
}

// Private final grant reconciliation. Run after database/credential preparation
// that needs bootstrap creator grants and before LOGIN activation. This helper
// never publishes stage readiness or authorizes replay of imported data.
func (s *server) projectEnvironmentClonePostgresMemberships(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan) (copyroles.MembershipReceipt, error) {
	var zero copyroles.MembershipReceipt
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	plan, owner, target, err := s.projectEnvironmentClonePostgresMembershipPlan(ctx, l, source)
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
	store := s.store.(state.ProjectEnvironmentClonePostgresMembershipPlanStore)
	authorize := func(checkCtx context.Context, actual copyarchive.RestoreTarget) error {
		fresh, err := store.ProjectEnvironmentClonePostgresMembershipPlanForLease(checkCtx, l, source.source.ID)
		if err == nil && (actual != target || !sameClonePostgresMembershipPlan(fresh, owner)) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	if err := authorize(ctx, target); err != nil {
		return zero, err
	}
	var result copyroles.MembershipReceipt
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			var err error
			result, err = copyroles.ApplyMemberships(sqlCtx, conn, plan, authorize)
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

func sameClonePostgresMembershipPlan(a, b state.ProjectEnvironmentClonePostgresMembershipPlan) bool {
	return a.TargetDatabaseID == b.TargetDatabaseID && a.InventoryCiphertextSHA256 == b.InventoryCiphertextSHA256 && a.TargetPinsCiphertextSHA256 == b.TargetPinsCiphertextSHA256 && a.RolePlanCiphertextSHA256 == b.RolePlanCiphertextSHA256 && a.CapturedAt.Equal(b.CapturedAt) &&
		a.Sealed.Scope.Equal(b.Sealed.Scope) && a.Sealed.InventoryFingerprint == b.Sealed.InventoryFingerprint && a.Sealed.TargetFingerprint == b.Sealed.TargetFingerprint && a.Sealed.KeyID == b.Sealed.KeyID &&
		a.Sealed.CiphertextSHA256 == b.Sealed.CiphertextSHA256 && bytes.Equal(a.Sealed.Ciphertext, b.Sealed.Ciphertext)
}
