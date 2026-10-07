package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func productionWorkUUID(value string) (pgtype.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}, ErrInvalidArgument
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func (s *PgStore) ProductionQueueInvocationByID(ctx context.Context, id string) (Invocation, error) {
	parsed, err := productionWorkUUID(id)
	if err != nil {
		return Invocation{}, err
	}
	row, err := sqlc.New().ReadProductionQueueInvocation(ctx, s.pool, parsed)
	if err != nil {
		return Invocation{}, mapErr(err)
	}
	return invocationFromSQLC(row)
}

func (s *PgStore) productionQueueState(ctx context.Context, appID, queueName string, named bool) (QueueStats, error) {
	id, err := productionWorkUUID(appID)
	if err != nil {
		return QueueStats{}, err
	}
	q := sqlc.New()
	live, err := q.ReadProductionQueueStateLive(ctx, s.pool, sqlc.ReadProductionQueueStateLiveParams{AppID: id, QueueName: queueName, Named: named})
	if err != nil {
		return QueueStats{}, err
	}
	dead, err := q.CountProductionQueueDeadLetter(ctx, s.pool, sqlc.CountProductionQueueDeadLetterParams{AppID: id, QueueName: queueName, Named: named})
	if err != nil {
		return QueueStats{}, err
	}
	stats := QueueStats{Depth: int(live.Depth), InFlight: int(live.InFlight), DeadLetter: int(dead)}
	if live.OldestPendingAt.Valid {
		stats.OldestPendingAt = live.OldestPendingAt.Time
	}
	return stats, nil
}

func productionQueueCursor(appID, before string, limit int) (pgtype.UUID, pgtype.UUID, int64, error) {
	app, err := productionWorkUUID(appID)
	if err != nil {
		return app, pgtype.UUID{}, 0, err
	}
	var cursor pgtype.UUID
	if before != "" {
		cursor, err = productionWorkUUID(before)
		if err != nil {
			return app, cursor, 0, err
		}
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	return app, cursor, int64(limit), nil
}
