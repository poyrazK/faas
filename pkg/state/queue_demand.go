// adr: 20261022105723
package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// maxQueueDepthLimit bounds a scaling reader's count. No scheduler decision
// needs more: worker pools and push lanes stop far below it.
const maxQueueDepthLimit = 1 << 20

func queueDepthLimit(limit int) (int32, error) {
	if limit <= 0 {
		return 0, ErrInvalidArgument
	}
	return int32(min(limit, maxQueueDepthLimit)), nil
}

func (s *PgStore) QueueDepthInScope(ctx context.Context, appID, scope string, limit int) (int, error) {
	app, err := uuid.Parse(appID)
	if err != nil || !validQueueStatsScope(scope) {
		return 0, ErrInvalidArgument
	}
	rows, err := queueDepthLimit(limit)
	if err != nil {
		return 0, err
	}
	depth, err := sqlc.New().QueueDepthInScope(ctx, s.pool, sqlc.QueueDepthInScopeParams{
		AppID: pgtype.UUID{Bytes: app, Valid: true}, DeploymentScope: scope, RowLimit: rows,
	})
	if err != nil {
		return 0, mapErr(err)
	}
	return min(int(depth), limit), nil
}

func (s *PgStore) QueueDepthForBindingInScope(ctx context.Context, appID, bindingID, scope string, limit int) (int, error) {
	app, binding, err := parseQueueBindingScope(appID, bindingID, scope)
	if err != nil {
		return 0, err
	}
	rows, err := queueDepthLimit(limit)
	if err != nil {
		return 0, err
	}
	depth, err := sqlc.New().QueueDepthForBinding(ctx, s.pool, sqlc.QueueDepthForBindingParams{
		AppID: app, BindingID: binding, RowLimit: rows,
		DeploymentScope: pgtype.Text{String: scope, Valid: scope != ""},
	})
	if err != nil {
		return 0, mapErr(err)
	}
	return min(int(depth), limit), nil
}

func (s *PgStore) QueueBindingHasInFlight(ctx context.Context, appID, bindingID, scope string) (bool, error) {
	app, binding, err := parseQueueBindingScope(appID, bindingID, scope)
	if err != nil {
		return false, err
	}
	inFlight, err := sqlc.New().QueueBindingHasInFlight(ctx, s.pool, sqlc.QueueBindingHasInFlightParams{
		AppID: app, BindingID: binding, DeploymentScope: pgtype.Text{String: scope, Valid: scope != ""},
	})
	if err != nil {
		return false, mapErr(err)
	}
	return inFlight, nil
}

func (s *PgStore) QueueBindingTelemetry(ctx context.Context, appID, bindingID string, limit int) (QueueStats, error) {
	app, binding, err := parseQueueBindingScope(appID, bindingID, "")
	if err != nil {
		return QueueStats{}, err
	}
	rows, err := queueDepthLimit(limit)
	if err != nil {
		return QueueStats{}, err
	}
	row, err := sqlc.New().QueueBindingTelemetry(ctx, s.pool, sqlc.QueueBindingTelemetryParams{AppID: app, BindingID: binding, RowLimit: rows})
	if err != nil {
		return QueueStats{}, mapErr(err)
	}
	stats := QueueStats{Depth: min(int(row.Depth), limit), InFlight: int(row.InFlight), DeadLetter: int(row.DeadLetter)}
	if row.OldestPendingAt.Valid {
		stats.OldestPendingAt = row.OldestPendingAt.Time
	}
	return stats, nil
}

func parseQueueBindingScope(appID, bindingID, scope string) (pgtype.UUID, pgtype.UUID, error) {
	app, err := uuid.Parse(appID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidArgument
	}
	binding, err := uuid.Parse(bindingID)
	if err != nil || scope != "" && !validQueueStatsScope(scope) {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidArgument
	}
	return pgtype.UUID{Bytes: app, Valid: true}, pgtype.UUID{Bytes: binding, Valid: true}, nil
}

func (m *MemStore) QueueDepthInScope(ctx context.Context, appID, scope string, limit int) (int, error) {
	if _, err := queueDepthLimit(limit); err != nil {
		return 0, err
	}
	stats, err := m.QueueStateInScope(ctx, appID, scope)
	return min(stats.Depth, limit), err
}

func (m *MemStore) QueueDepthForBindingInScope(_ context.Context, appID, bindingID, scope string, limit int) (int, error) {
	if _, err := queueDepthLimit(limit); err != nil {
		return 0, err
	}
	if scope != "" && !validQueueStatsScope(scope) {
		return 0, ErrInvalidArgument
	}
	stats, err := m.queueStateForBinding(appID, bindingID, scope)
	return min(stats.Depth, limit), err
}

func (m *MemStore) QueueBindingHasInFlight(_ context.Context, appID, bindingID, scope string) (bool, error) {
	if scope != "" && !validQueueStatsScope(scope) {
		return false, ErrInvalidArgument
	}
	stats, err := m.queueStateForBinding(appID, bindingID, scope)
	return stats.InFlight > 0, err
}

func (m *MemStore) QueueBindingTelemetry(_ context.Context, appID, bindingID string, limit int) (QueueStats, error) {
	if _, err := queueDepthLimit(limit); err != nil {
		return QueueStats{}, err
	}
	stats, err := m.queueStateForBinding(appID, bindingID, "")
	stats.Depth, stats.InFlight, stats.DeadLetter = min(stats.Depth, limit), min(stats.InFlight, limit), min(stats.DeadLetter, limit)
	return stats, err
}
