package neon

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.SnapshotCopyReaderDatabaseSQLProvider = (*Provider)(nil)

func (p *Provider) WithSnapshotCopyReaderDatabaseSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDatabaseSQLRequest, read managedpostgres.SnapshotCopyReaderSQLRead) error {
	return p.withSnapshotCopyReaderDatabaseSQL(ctx, d, r, read, pgx.ConnectConfig)
}

func (p *Provider) withSnapshotCopyReaderDatabaseSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDatabaseSQLRequest, read managedpostgres.SnapshotCopyReaderSQLRead, connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) error {
	if err := r.Validate(d); err != nil {
		return err
	}
	return p.borrowSnapshotCopyReaderSQL(ctx, d, r.Reader, &r.Database, read, connect)
}
