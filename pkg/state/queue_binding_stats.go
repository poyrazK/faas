package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) QueueStateForBinding(ctx context.Context, appID, bindingID string) (QueueStats, error) {
	return s.queueStateForBinding(ctx, appID, bindingID, "")
}

func (s *PgStore) QueueStateForBindingInScope(ctx context.Context, appID, bindingID, scope string) (QueueStats, error) {
	if !validQueueStatsScope(scope) {
		return QueueStats{}, ErrInvalidArgument
	}
	return s.queueStateForBinding(ctx, appID, bindingID, scope)
}

func (s *PgStore) queueStateForBinding(ctx context.Context, appID, bindingID, scope string) (QueueStats, error) {
	app, err := uuid.Parse(appID)
	if err != nil {
		return QueueStats{}, ErrInvalidArgument
	}
	binding, err := uuid.Parse(bindingID)
	if err != nil {
		return QueueStats{}, ErrInvalidArgument
	}
	row, err := sqlc.New().QueueStateForBinding(ctx, s.pool, sqlc.QueueStateForBindingParams{
		AppID: pgtype.UUID{Bytes: app, Valid: true}, BindingID: pgtype.UUID{Bytes: binding, Valid: true},
		DeploymentScope: pgtype.Text{String: scope, Valid: scope != ""},
	})
	if err != nil {
		return QueueStats{}, mapErr(err)
	}
	stats := QueueStats{Depth: int(row.Depth), InFlight: int(row.InFlight), DeadLetter: int(row.DeadLetter)}
	if row.OldestPendingAt.Valid {
		stats.OldestPendingAt = row.OldestPendingAt.Time
	}
	return stats, nil
}

func (m *MemStore) QueueStateForBinding(_ context.Context, appID, bindingID string) (QueueStats, error) {
	return m.queueStateForBinding(appID, bindingID, "")
}

func (m *MemStore) QueueStateForBindingInScope(_ context.Context, appID, bindingID, scope string) (QueueStats, error) {
	if !validQueueStatsScope(scope) {
		return QueueStats{}, ErrInvalidArgument
	}
	return m.queueStateForBinding(appID, bindingID, scope)
}

func (m *MemStore) queueStateForBinding(appID, bindingID, scope string) (QueueStats, error) {
	if _, err := uuid.Parse(appID); err != nil {
		return QueueStats{}, ErrInvalidArgument
	}
	parsed, err := uuid.Parse(bindingID)
	if err != nil {
		return QueueStats{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var binding QueueBinding
	for _, row := range m.queueBindings {
		if row.AppID == appID && canonicalMemUUID(row.ID) == parsed.String() {
			binding = row
			break
		}
	}
	stats := QueueStats{}
	if binding.ID == "" {
		return stats, nil
	}
	now := time.Now()
	for _, inv := range m.invocations {
		if inv.AppID != appID || inv.AccountID != binding.AccountID || inv.Source != InvocationQueue ||
			scope != "" && inv.DeploymentScope != scope ||
			!(inv.QueueBindingID == binding.ID || inv.QueueBindingID == "" && inv.QueueName == binding.QueueName) {
			continue
		}
		switch inv.State {
		case InvocationPending:
			stats.Depth++
			if stats.OldestPendingAt.IsZero() || inv.CreatedAt.Before(stats.OldestPendingAt) {
				stats.OldestPendingAt = inv.CreatedAt
			}
		case InvocationDispatching:
			stats.Depth++
			if inv.LeaseExpiresAt != nil && inv.LeaseExpiresAt.After(now) {
				stats.InFlight++
			}
		case InvocationDeadLetter:
			stats.DeadLetter++
		}
	}
	return stats, nil
}
