package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func productionDeadLetterLimit(limit int) int64 {
	if limit <= 0 {
		limit = deadLetterEventsDefaultLimit
	}
	if limit > deadLetterEventsMaxLimit {
		limit = deadLetterEventsMaxLimit
	}
	return int64(limit)
}

// At least one owner is mandatory. Empty app is reserved for account-wide calls.
func productionDeadLetterScope(accountID, appID, eventID string) (sqlc.ReadProductionDeadLetterEventParams, error) {
	var args sqlc.ReadProductionDeadLetterEventParams
	if accountID == "" && appID == "" {
		return args, ErrInvalidArgument
	}
	for _, field := range []struct {
		value  string
		target *pgtype.UUID
	}{{accountID, &args.AccountID}, {appID, &args.AppID}, {eventID, &args.EventID}} {
		if field.value == "" {
			continue
		}
		id, err := productionWorkUUID(field.value)
		if err != nil {
			return args, err
		}
		*field.target = id
	}
	return args, nil
}

func deadLetterEventFromSQLC(row sqlc.DeadLetterEvent) DeadLetterEvent {
	id := func(v pgtype.UUID) string {
		if !v.Valid {
			return ""
		}
		return uuid.UUID(v.Bytes).String()
	}
	ev := DeadLetterEvent{ID: id(row.ID), AccountID: id(row.AccountID), AppID: id(row.AppID), Source: row.Source,
		SourceID: id(row.SourceID), Origin: row.Origin, TriggerID: id(row.TriggerID), Payload: row.EventPayload,
		Headers: row.Headers, ErrorKind: row.ErrorKind, ErrorDetail: row.ErrorDetail, RetryCount: int(row.RetryCount),
		FirstFailedAt: row.FirstFailedAt.Time, LastFailedAt: row.LastFailedAt.Time, CreatedAt: row.CreatedAt.Time}
	if row.ReplayedAt.Valid {
		t := row.ReplayedAt.Time
		ev.ReplayedAt = &t
	}
	return ev
}

func deadLetterEventsFromSQLC(rows []sqlc.DeadLetterEvent) []DeadLetterEvent {
	out := make([]DeadLetterEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, deadLetterEventFromSQLC(row))
	}
	return out
}

func (s *PgStore) productionDeadLetterPage(ctx context.Context, accountID, appID string, limit int, before string) ([]DeadLetterEvent, error) {
	args, err := productionDeadLetterScope(accountID, appID, before)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListProductionDeadLetterEvents(ctx, s.pool, sqlc.ListProductionDeadLetterEventsParams{
		AccountID: args.AccountID, AppID: args.AppID, CursorID: args.EventID, PageLimit: productionDeadLetterLimit(limit)})
	return deadLetterEventsFromSQLC(rows), err
}

func (s *PgStore) productionDeadLetterRead(ctx context.Context, accountID, appID, eventID string) (DeadLetterEvent, error) {
	args, err := productionDeadLetterScope(accountID, appID, eventID)
	if err != nil {
		return DeadLetterEvent{}, err
	}
	row, err := sqlc.New().ReadProductionDeadLetterEvent(ctx, s.pool, args)
	return deadLetterEventFromSQLC(row), mapErr(err)
}

func (s *PgStore) productionDeadLetterReplay(ctx context.Context, accountID, appID, eventID string) (DeadLetterEvent, error) {
	args, err := productionDeadLetterScope(accountID, appID, eventID)
	if err != nil {
		return DeadLetterEvent{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeadLetterEvent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().LockProductionDeadLetterEvent(ctx, tx, sqlc.LockProductionDeadLetterEventParams(args))
	if err != nil {
		return DeadLetterEvent{}, mapErr(err)
	}
	ev := deadLetterEventFromSQLC(row)
	now, err := replayDeadLetterEventTx(ctx, tx, accountID, ev.AppID, ev)
	if err != nil {
		return DeadLetterEvent{}, err
	}
	ev.ReplayedAt = &now
	if err := tx.Commit(ctx); err != nil {
		return DeadLetterEvent{}, err
	}
	return ev, nil
}

func (s *PgStore) productionDeadLetterReplayMany(ctx context.Context, accountID, appID string, limit int) (int, error) {
	args, err := productionDeadLetterScope(accountID, appID, "")
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := sqlc.New().LockProductionDeadLetterEvents(ctx, tx, sqlc.LockProductionDeadLetterEventsParams{
		AccountID: args.AccountID, AppID: args.AppID, OpenOnly: true, PageLimit: productionDeadLetterLimit(limit)})
	if err != nil {
		return 0, err
	}
	replayed := 0
	for _, row := range rows {
		ev := deadLetterEventFromSQLC(row)
		if _, err := replayDeadLetterEventTx(ctx, tx, accountID, ev.AppID, ev); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return 0, err
		}
		replayed++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return replayed, nil
}

func (s *PgStore) productionDeadLetterDelete(ctx context.Context, accountID, appID, eventID string) error {
	args, err := productionDeadLetterScope(accountID, appID, eventID)
	if err != nil {
		return err
	}
	n, err := sqlc.New().DeleteProductionDeadLetterEvent(ctx, s.pool, sqlc.DeleteProductionDeadLetterEventParams(args))
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) productionDeadLetterDeleteMany(ctx context.Context, accountID, appID string, limit int) (int, error) {
	args, err := productionDeadLetterScope(accountID, appID, "")
	if err != nil {
		return 0, err
	}
	n, err := sqlc.New().DeleteProductionDeadLetterEvents(ctx, s.pool, sqlc.DeleteProductionDeadLetterEventsParams{
		AccountID: args.AccountID, AppID: args.AppID, PageLimit: productionDeadLetterLimit(limit)})
	return int(n), err
}
