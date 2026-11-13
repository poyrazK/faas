package datapipeline

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// The marker is transaction-local: an N-1 worker borrowing this connection must
// not inherit permission to claim a serving contract it cannot enforce.
func (s *PostgresStore) workerClaim(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.q.PipelineWorkerProtocol(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
