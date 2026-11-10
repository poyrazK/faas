package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	_ RequestTelemetryLogStore      = (*PgStore)(nil)
	_ RequestTelemetryBatchLogStore = (*PgStore)(nil)
)

// InsertRequestTelemetryWithLogEvent atomically adds one accepted telemetry
// aggregate and its redacted HTTP log projection. The log ledger's unique
// source key gates both writes when the publisher replays a batch.
func (s *PgStore) InsertRequestTelemetryWithLogEvent(ctx context.Context, arg sqlc.InsertRequestTelemetryParams, eventID string) error {
	return s.InsertRequestTelemetryBatchWithLogEvents(ctx, []RequestTelemetryInsert{{Params: arg, EventID: eventID}})
}

// InsertRequestTelemetryBatchWithLogEvents adds a group of accepted
// telemetry rows and their HTTP log projections in one transaction, so a
// gateway batch costs one commit instead of one per row. The transaction
// commits with synchronous_commit off: telemetry and its log projection are
// debugger evidence, never billing (usage has its own outbox and ledger),
// so a Postgres crash may lose the last moments of acknowledged rows rather
// than every row waiting on a WAL flush (ADR-127 amendment, group commit).
func (s *PgStore) InsertRequestTelemetryBatchWithLogEvents(ctx context.Context, rows []RequestTelemetryInsert) error {
	if len(rows) == 0 {
		return nil
	}
	events := make([]LogEvent, len(rows))
	for i, row := range rows {
		event, err := requestTelemetryLogEvent(row.Params, row.EventID)
		if err != nil {
			return err
		}
		events[i] = event
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin HTTP log projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit = off"); err != nil {
		return fmt.Errorf("relax request telemetry commit: %w", err)
	}
	for i, row := range rows {
		if err := s.insertRequestTelemetryLogged(ctx, tx, row.Params, events[i]); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit HTTP log projection: %w", err)
	}
	return nil
}

// insertRequestTelemetryLogged adds the log projection and, unless the
// ledger already holds the event (a replay), the telemetry row.
func (s *PgStore) insertRequestTelemetryLogged(ctx context.Context, tx pgx.Tx, arg sqlc.InsertRequestTelemetryParams, event LogEvent) error {
	persisted, err := insertLogEventRow(ctx, tx, event)
	if err != nil {
		return fmt.Errorf("insert HTTP log projection: %w", err)
	}
	if persisted.ID == event.ID {
		if err := s.appErrorsQueries().InsertRequestTelemetry(ctx, tx, arg); err != nil {
			return fmt.Errorf("insert request telemetry with HTTP log: %w", mapErr(err))
		}
	}
	return nil
}
