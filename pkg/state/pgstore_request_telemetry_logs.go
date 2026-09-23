package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RequestTelemetryLogStore = (*PgStore)(nil)

// InsertRequestTelemetryWithLogEvent atomically adds one accepted telemetry
// aggregate and its redacted HTTP log projection. The log ledger's unique
// source key gates both writes when the publisher replays a batch.
func (s *PgStore) InsertRequestTelemetryWithLogEvent(ctx context.Context, arg sqlc.InsertRequestTelemetryParams, eventID string) error {
	event, err := requestTelemetryLogEvent(arg, eventID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin HTTP log projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	persisted, err := insertLogEventRow(ctx, tx, event)
	if err != nil {
		return fmt.Errorf("insert HTTP log projection: %w", err)
	}
	if persisted.ID == event.ID {
		if err := s.appErrorsQueries().InsertRequestTelemetry(ctx, tx, arg); err != nil {
			return fmt.Errorf("insert request telemetry with HTTP log: %w", mapErr(err))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit HTTP log projection: %w", err)
	}
	return nil
}
