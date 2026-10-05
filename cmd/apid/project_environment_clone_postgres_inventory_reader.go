package main

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// This private reader connects only for a missing sealed inventory. Recovery
// opens the retained receipt without contacting a current source or reader.
func (s *server) readProjectEnvironmentClonePostgresInventory(ctx context.Context, lease state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentDatabasePlan) (copyinventory.Inventory, error) {
	return s.projectEnvironmentClonePostgresInventory(ctx, lease, plan.source.ID, func(readCtx context.Context, scope copyinventory.Scope, key [32]byte) (copyinventory.Config, copyinventory.Inventory, error) {
		readers, readerOK := s.store.(state.ProjectEnvironmentClonePostgresCopyReaderStore)
		snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
		captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
		if !readerOK || !snapshotOK || !captureOK || s.managedPostgres == nil {
			return copyinventory.Config{}, copyinventory.Inventory{}, managedpostgres.ErrUnavailable
		}
		reader, err := readers.ProjectEnvironmentClonePostgresCopyReaderForLease(readCtx, lease, plan.source.ID)
		if err != nil {
			return copyinventory.Config{}, copyinventory.Inventory{}, err
		}
		if reader.State != "observed" || !reader.Available || reader.EndpointID == "" || !reader.Scope.Equal(scope) {
			return copyinventory.Config{}, copyinventory.Inventory{}, managedpostgres.ErrConflict
		}
		snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(readCtx, lease, plan.source.ID)
		if err == nil {
			err = validateClonePostgresSnapshotPlan(lease.Operation, plan, snapshot)
		}
		if err == nil && snapshot.State != "retained" {
			err = state.ErrConflict
		}
		if err != nil {
			return copyinventory.Config{}, copyinventory.Inventory{}, err
		}
		capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(readCtx, lease, plan.source.ID)
		if err != nil {
			return copyinventory.Config{}, copyinventory.Inventory{}, err
		}
		if capture.State != "adopted" || capture.AdoptedDatabaseID != scope.CaptureDatabaseID {
			return copyinventory.Config{}, copyinventory.Inventory{}, state.ErrConflict
		}
		var cfg copyinventory.Config
		var inventory copyinventory.Inventory
		err = s.managedPostgres.WithSnapshotCopyReaderSQL(readCtx, clonePostgresSnapshotDefinition(plan), clonePostgresCopyReaderRequest(reader, snapshot, capture),
			func(sqlCtx context.Context, conn *pgx.Conn, identity managedpostgres.SnapshotCopyReaderSQLIdentity) error {
				if identity.PostgresMajor != scope.PostgresMajor {
					return managedpostgres.ErrConflict
				}
				cfg = copyinventory.Config{PostgresMajor: identity.PostgresMajor, DatabaseName: identity.DatabaseName, RoleName: identity.RoleName,
					DatabaseOID: identity.DatabaseOID, RoleOID: identity.RoleOID, FingerprintKey: key}
				var err error
				inventory, err = copyinventory.Read(sqlCtx, conn, cfg)
				return err
			})
		return cfg, inventory, err
	})
}
