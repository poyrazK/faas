package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Database workers require the original projected export plan explicitly. The
// role-only helper cannot reconstruct pre-fence admission intent. Verify every
// retained capture byte/scope before using that logical projection for databases.
func (s *server) clonePostgresDatabaseInput(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan, ids []*age.X25519Identity, exports copyinventory.ExportPlan) (clonePostgresRoleInput, error) {
	i, err := s.clonePostgresRoleInput(ctx, l, source, ids)
	if err != nil {
		return i, err
	}
	if !exports.SameCaptureForWorker(i.source) {
		return clonePostgresRoleInput{}, managedpostgres.ErrConflict
	}
	i.source = exports
	return i, nil
}

func (i clonePostgresRoleInput) openDatabases(ids []*age.X25519Identity, roleOwner state.ProjectEnvironmentClonePostgresRolePlan, owner state.ProjectEnvironmentClonePostgresDatabasePlan) (copydatabases.Plan, error) {
	if owner.TargetDatabaseID != i.target.OwnerID || owner.InventoryCiphertextSHA256 != i.inventory.Sealed.CiphertextSHA256 || owner.TargetPinsCiphertextSHA256 != i.pins.Sealed.CiphertextSHA256 || owner.RolePlanCiphertextSHA256 != roleOwner.Sealed.CiphertextSHA256 {
		return copydatabases.Plan{}, managedpostgres.ErrConflict
	}
	parent, err := i.open(ids, roleOwner)
	if err != nil {
		return copydatabases.Plan{}, err
	}
	p, err := copydatabases.Open(ids, i.source, i.target, owner.Sealed)
	if err == nil && !p.MatchesRolePlanForWorker(i.source, parent) {
		err = managedpostgres.ErrConflict
	}
	return p, err
}

// The first complete encrypted plan wins before CREATE DATABASE. Retried capture
// opens original OIDs/baseline without target SQL or a current recipient. The
// explicit export plan must include authenticated original source admission.
func (s *server) projectEnvironmentClonePostgresDatabasePlan(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan) (copydatabases.Plan, state.ProjectEnvironmentClonePostgresDatabasePlan, copyarchive.RestoreTarget, error) {
	var zero copydatabases.Plan
	var owner state.ProjectEnvironmentClonePostgresDatabasePlan
	var target copyarchive.RestoreTarget
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresDatabasePlanStore)
	if !ok || mfaIdentities == nil {
		return zero, owner, target, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	_, roleOwner, target, err := s.projectEnvironmentClonePostgresRolePlan(ctx, l, source)
	if err != nil {
		return zero, owner, target, err
	}
	ids := mfaIdentities()
	input, err := s.clonePostgresDatabaseInput(ctx, l, source, ids, exports)
	if err != nil || input.target != target {
		if err == nil {
			err = managedpostgres.ErrConflict
		}
		return zero, owner, target, err
	}
	owner, err = store.ProjectEnvironmentClonePostgresDatabasePlanForLease(ctx, l, source.source.ID)
	if err == nil {
		p, e := input.openDatabases(ids, roleOwner, owner)
		return p, owner, target, e
	}
	if !errors.Is(err, state.ErrNotFound) {
		return zero, owner, target, err
	}
	if l.Operation.Status != state.CloneOperationCapturing || s.managedPostgres == nil || setSecretRecipient == nil {
		return zero, owner, target, managedpostgres.ErrUnavailable
	}
	recipient := setSecretRecipient()
	if !cloneInventoryCanOpen(recipient, ids) {
		return zero, owner, target, managedpostgres.ErrUnavailable
	}
	seed, err := s.projectEnvironmentClonePostgresRoles(ctx, l, source)
	if err != nil {
		return zero, owner, target, err
	}
	parent, err := input.open(ids, roleOwner)
	if err != nil || !seed.MatchesPlanForWorker(parent) {
		if err == nil {
			err = managedpostgres.ErrConflict
		}
		return zero, owner, target, err
	}
	roleStore := s.store.(state.ProjectEnvironmentClonePostgresRolePlanStore)
	authorize := func(checkCtx context.Context) error {
		fresh, err := roleStore.ProjectEnvironmentClonePostgresRolePlanForLease(checkCtx, l, source.source.ID)
		if err == nil && !sameClonePostgresRolePlan(fresh, roleOwner) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	if err = authorize(ctx); err != nil {
		return zero, owner, target, err
	}
	request, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, target.Scope, target)
	if err != nil {
		return zero, owner, target, err
	}
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		return zero, owner, target, managedpostgres.ErrUnavailable
	}
	var plan copydatabases.Plan
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(sqlCtx context.Context, conn *pgx.Conn, id managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			if err := authorize(sqlCtx); err != nil {
				return err
			}
			if err := seed.VerifyForWorker(sqlCtx, conn); err != nil {
				return err
			}
			inventory, err := copyinventory.Read(sqlCtx, conn, copyinventory.Config{PostgresMajor: id.PostgresMajor, DatabaseName: id.DatabaseName, DatabaseOID: id.DatabaseOID, RoleName: id.RoleName, RoleOID: id.RoleOID, FingerprintKey: key})
			if err != nil {
				return err
			}
			dbs, spaces, err := clonePostgresDatabaseDispositions(input.source, inventory)
			if err == nil {
				plan, err = copydatabases.NewPlan(input.source, seed, inventory, dbs, spaces)
			}
			return err
		})
	if err != nil {
		return zero, owner, target, err
	}
	sealed, err := copydatabases.Seal(recipient, plan)
	if err != nil {
		return zero, owner, target, err
	}
	owner, _, err = store.RecordProjectEnvironmentClonePostgresDatabasePlan(ctx, l, source.source.ID, roleOwner.Sealed.CiphertextSHA256, sealed)
	if err != nil {
		return zero, state.ProjectEnvironmentClonePostgresDatabasePlan{}, target, err
	}
	plan, err = input.openDatabases(ids, roleOwner, owner)
	return plan, owner, target, err
}

// Prepare every captured database from one durable complete plan. Partial target
// progress remains owned and recoverable, but an error returns no successful set.
// These receipts are neither per-database import authority nor stage readiness.
func (s *server) projectEnvironmentClonePostgresDatabases(ctx context.Context, l state.ProjectEnvironmentCloneLease, source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan) ([]copydatabases.Receipt, error) {
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	plan, owner, target, err := s.projectEnvironmentClonePostgresDatabasePlan(ctx, l, source, exports)
	if err != nil {
		return nil, err
	}
	if s.managedPostgres == nil {
		return nil, managedpostgres.ErrUnavailable
	}
	request, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, target.Scope, target)
	if err != nil {
		return nil, err
	}
	store := s.store.(state.ProjectEnvironmentClonePostgresDatabasePlanStore)
	authorize := func(checkCtx context.Context, actual copyarchive.RestoreTarget) error {
		fresh, err := store.ProjectEnvironmentClonePostgresDatabasePlanForLease(checkCtx, l, source.source.ID)
		if err == nil && (actual != target || !sameClonePostgresDatabasePlan(fresh, owner)) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	if err = authorize(ctx, target); err != nil {
		return nil, err
	}
	requirements, err := exports.RequirementsForWorker()
	if err != nil {
		return nil, err
	}
	result := make([]copydatabases.Receipt, 0, len(requirements))
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(sqlCtx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			for _, d := range requirements {
				r, err := copydatabases.Prepare(sqlCtx, conn, exports, plan, d.Database.OID, authorize)
				if err != nil {
					return err
				}
				result = append(result, r)
			}
			return nil
		})
	if err == nil {
		err = authorize(ctx, target)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func sameClonePostgresDatabasePlan(a, b state.ProjectEnvironmentClonePostgresDatabasePlan) bool {
	return a.TargetDatabaseID == b.TargetDatabaseID && a.InventoryCiphertextSHA256 == b.InventoryCiphertextSHA256 && a.TargetPinsCiphertextSHA256 == b.TargetPinsCiphertextSHA256 && a.RolePlanCiphertextSHA256 == b.RolePlanCiphertextSHA256 && a.CapturedAt.Equal(b.CapturedAt) &&
		a.Sealed.Scope.Equal(b.Sealed.Scope) && a.Sealed.InventoryFingerprint == b.Sealed.InventoryFingerprint && a.Sealed.TargetFingerprint == b.Sealed.TargetFingerprint && a.Sealed.KeyID == b.Sealed.KeyID && a.Sealed.CiphertextSHA256 == b.Sealed.CiphertextSHA256 && bytes.Equal(a.Sealed.Ciphertext, b.Sealed.Ciphertext)
}

func clonePostgresDatabaseDispositions(source copyinventory.ExportPlan, target copyinventory.Inventory) ([]copydatabases.Disposition, []copydatabases.TablespaceMapping, error) {
	src, err := source.DatabaseCatalogueForWorker()
	if err != nil {
		return nil, nil, err
	}
	base, err := target.DatabaseCatalogueForWorker()
	if err != nil {
		return nil, nil, err
	}
	used, byName := map[uint32]bool{}, map[string]uint32{}
	for _, d := range src.Databases {
		used[d.OID] = true
	}
	for _, d := range base.Databases {
		used[d.OID] = true
		byName[d.Name] = d.OID
	}
	var entropy [4]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return nil, nil, managedpostgres.ErrUnavailable
	}
	// The reserved-OID boundary is PostgreSQL's catalogue domain, not a quota.
	next := binary.BigEndian.Uint32(entropy[:])
	if next < 16384 {
		next = 16384
	}
	dbs := make([]copydatabases.Disposition, 0, len(src.Databases))
	for _, d := range src.Databases {
		choice := copydatabases.Disposition{SourceOID: d.OID, ExistingTargetOID: byName[d.Name]}
		if choice.ExistingTargetOID == 0 {
			for used[next] || next < 16384 {
				next++
				if next < 16384 {
					next = 16384
				}
			}
			choice.CreateTargetOID = next
			used[next] = true
			next++
		}
		dbs = append(dbs, choice)
	}
	spaces := make([]copydatabases.TablespaceMapping, 0, len(src.Tablespaces))
	for _, s := range src.Tablespaces {
		var id uint32
		for _, d := range base.Tablespaces {
			if s.Name == d.Name {
				id = d.OID
			}
		}
		spaces = append(spaces, copydatabases.TablespaceMapping{SourceOID: s.OID, TargetOID: id})
	}
	return dbs, spaces, nil
}
