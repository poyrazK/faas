package neon

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	inventorysql "github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory/sqlc"
)

var _ managedpostgres.SnapshotCopyReaderSQLProvider = (*Provider)(nil)

func (p *Provider) WithSnapshotCopyReaderSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest, read managedpostgres.SnapshotCopyReaderSQLRead) error {
	return p.withSnapshotCopyReaderSQL(ctx, d, r, read, pgx.ConnectConfig)
}

// The connector argument is private to qualification tests. Production always
// uses the minimal verify-full configuration with the exact readonly endpoint.
func (p *Provider) withSnapshotCopyReaderSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest, read managedpostgres.SnapshotCopyReaderSQLRead, connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) error {
	return p.borrowSnapshotCopyReaderSQL(ctx, d, r, nil, read, connect)
}

func (p *Provider) borrowSnapshotCopyReaderSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest, selected *copyinventory.DatabaseExport, read managedpostgres.SnapshotCopyReaderSQLRead, connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) error {
	if read == nil || connect == nil {
		return managedpostgres.ErrInvalid
	}
	config, err := p.snapshotCopyReaderConnectionConfig(ctx, d, r)
	if err != nil {
		return err
	}
	if selected != nil {
		// The URI authenticates credentials and placement through the fixed
		// maintenance database. Select the retained SQL name literally rather
		// than interpreting it as URL path/query/options or a new endpoint.
		config.Database = selected.Database.Name
	}
	expected := config.Copy()
	conn, err := connect(ctx, config)
	if conn != nil {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			_ = conn.Close(cleanup)
		}()
	}
	if err != nil {
		return maintenanceConnectionError(ctx, err)
	}
	if conn == nil {
		return managedpostgres.ErrConflict
	}
	identity, err := authenticateSnapshotCopyReaderSQL(ctx, conn, expected, d.Spec.PostgresMajor)
	if err != nil {
		return err
	}
	if selected != nil && (identity.DatabaseName != selected.Database.Name || identity.DatabaseOID != selected.Database.OID || identity.RoleOID != selected.AuthenticatedReaderRoleOID) {
		return managedpostgres.ErrConflict
	}
	before, err := p.snapshotCopyReaderConnectionPlacement(ctx, d, r)
	if err != nil {
		return err
	}
	if before.Host != expected.Host {
		return managedpostgres.ErrConflict
	}
	if err := read(ctx, conn, identity); err != nil {
		return err
	}
	observed, err := authenticateSnapshotCopyReaderSQL(ctx, conn, expected, d.Spec.PostgresMajor)
	if err != nil {
		return err
	}
	if observed != identity {
		return managedpostgres.ErrConflict
	}
	after, err := p.snapshotCopyReaderConnectionPlacement(ctx, d, r)
	if err != nil {
		return err
	}
	if after.Host != expected.Host {
		return managedpostgres.ErrConflict
	}
	return nil
}

func authenticateSnapshotCopyReaderSQL(ctx context.Context, conn *pgx.Conn, expected *pgx.ConnConfig, major int) (managedpostgres.SnapshotCopyReaderSQLIdentity, error) {
	if conn == nil || expected == nil || conn.IsClosed() || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' {
		return managedpostgres.SnapshotCopyReaderSQLIdentity{}, managedpostgres.ErrConflict
	}
	actual, err := inventorysql.New().CopyClusterIdentity(ctx, conn)
	if err != nil {
		return managedpostgres.SnapshotCopyReaderSQLIdentity{}, maintenanceConnectionError(ctx, err)
	}
	if int(actual.ServerVersion/10000) != major || actual.DatabaseName != expected.Database || actual.RoleName != expected.User ||
		actual.SessionRole != expected.User || !actual.ReadOnly || !actual.DatabaseOid.Valid || actual.DatabaseOid.Uint32 == 0 || !actual.RoleOid.Valid || actual.RoleOid.Uint32 == 0 {
		return managedpostgres.SnapshotCopyReaderSQLIdentity{}, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotCopyReaderSQLIdentity{PostgresMajor: major, DatabaseName: actual.DatabaseName, DatabaseOID: actual.DatabaseOid.Uint32,
		RoleName: actual.RoleName, RoleOID: actual.RoleOid.Uint32}, nil
}
