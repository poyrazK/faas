package connectionfence

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// DiscoverDatabaseSelection reads one complete native catalogue snapshot. It
// installs no SQL ledger and changes no connection flags. The clone owner must
// retain this selection before closure, and recheck coverage before capture;
// discovery alone cannot establish writer drainage or a common data point.
func (c *Controller) DiscoverDatabaseSelection(ctx context.Context, identity Identity) (Request, error) {
	if c == nil || c.pool == nil || !validIdentity(identity) {
		return Request{}, pgerrors.ErrInvalid
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Request{}, classifyError(err)
	}
	// Read-only cleanup shares the caller's deadline; failed rollback causes
	// pgx to discard the connection rather than retaining an open transaction.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := c.checkMaintenance(ctx, tx); err != nil {
		return Request{}, err
	}
	names, err := sqlc.New().CheckpointDatabaseNames(ctx, tx, sqlc.CheckpointDatabaseNamesParams{
		MaintenanceDatabase: c.config.MaintenanceDatabase, MaxDatabases: api.PostgresCheckpointDatabasesMax + 1,
	})
	if err != nil {
		return Request{}, classifyError(err)
	}
	if len(names) > api.PostgresCheckpointDatabasesMax {
		return Request{}, pgerrors.ErrQuotaExceeded
	}
	if len(names) == 0 {
		return Request{}, pgerrors.ErrConflict
	}
	for _, name := range names {
		if !validDatabaseName(name) || name == c.config.MaintenanceDatabase {
			return Request{}, pgerrors.ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, classifyError(err)
	}
	if err := c.checkMaintenance(ctx, c.pool); err != nil {
		return Request{}, err
	}
	if err := ctx.Err(); err != nil {
		return Request{}, err
	}
	return Request{Identity: identity, DatabaseNames: names}, nil
}
