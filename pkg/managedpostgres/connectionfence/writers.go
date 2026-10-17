package connectionfence

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpoint"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence/sqlc"
)

func readWriterInventory(ctx context.Context, tx pgx.Tx, q *sqlc.Queries) (*checkpoint.WriterInventory, error) {
	rows, err := q.CheckpointWriterCatalog(ctx, tx, api.PostgresCheckpointDatabasesMax+2)
	if err != nil {
		return nil, classifyError(err)
	}
	activity, err := q.CheckpointWriterActivity(ctx, tx)
	if err != nil {
		return nil, classifyError(err)
	}
	out := &checkpoint.WriterInventory{DatabaseCount: len(rows), CatalogComplete: len(rows) <= api.PostgresCheckpointDatabasesMax+1,
		ClientSessions: activity.ClientSessions, BackgroundWorkers: activity.BackgroundWorkers,
		ReplicationWorkers: activity.ReplicationWorkers, UnknownBackends: activity.UnknownBackends}
	if out.CatalogComplete {
		databases := make([]checkpoint.CatalogDatabase, 0, len(rows))
		for _, row := range rows {
			databases = append(databases, checkpoint.CatalogDatabase{OID: row.Oid.Uint32, OwnerOID: row.OwnerOid.Uint32,
				Name: row.DatabaseName, AllowConnections: row.Datallowconn, IsTemplate: row.Datistemplate, ConnectionLimit: row.Datconnlimit})
		}
		out.CatalogFingerprint, err = checkpoint.CatalogFingerprint(databases)
		if err != nil {
			return nil, err
		}
	}
	return out, out.Validate()
}
