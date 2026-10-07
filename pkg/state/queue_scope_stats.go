package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validQueueStatsScope(scope string) bool {
	return scope != "" && api.ValidateScope(scope) == nil
}

func (s *PgStore) QueueStateInScope(ctx context.Context, appID, scope string) (QueueStats, error) {
	return s.queueStateInScope(ctx, appID, nil, scope)
}

func (s *PgStore) QueueStateForQueueInScope(ctx context.Context, appID, queueName, scope string) (QueueStats, error) {
	return s.queueStateInScope(ctx, appID, &queueName, scope)
}

func (s *PgStore) queueStateInScope(ctx context.Context, appID string, queueName *string, scope string) (QueueStats, error) {
	appUUID, err := uuid.Parse(appID)
	if err != nil || !validQueueStatsScope(scope) {
		return QueueStats{}, ErrInvalidArgument
	}
	queue := pgtype.Text{}
	if queueName != nil {
		queue = pgtype.Text{String: *queueName, Valid: true}
	}
	row, err := sqlc.New().QueueStateInScope(ctx, s.pool, sqlc.QueueStateInScopeParams{
		AppID: pgtype.UUID{Bytes: appUUID, Valid: true}, DeploymentScope: scope, QueueName: queue,
	})
	if err != nil {
		return QueueStats{}, err
	}
	stats := QueueStats{Depth: int(row.Depth), InFlight: int(row.InFlight), DeadLetter: int(row.DeadLetter)}
	if row.OldestPendingAt.Valid {
		stats.OldestPendingAt = row.OldestPendingAt.Time
	}
	return stats, nil
}

func (m *MemStore) QueueStateInScope(_ context.Context, appID, scope string) (QueueStats, error) {
	return m.queueStateInScope(appID, nil, scope)
}

func (m *MemStore) QueueStateForQueueInScope(_ context.Context, appID, queueName, scope string) (QueueStats, error) {
	return m.queueStateInScope(appID, &queueName, scope)
}

func (m *MemStore) queueStateInScope(appID string, queueName *string, scope string) (QueueStats, error) {
	if !validQueueStatsScope(scope) {
		return QueueStats{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := QueueStats{}
	now := time.Now()
	for _, inv := range m.invocations {
		if inv.AppID != appID || inv.DeploymentScope != scope || inv.Source != InvocationQueue ||
			queueName != nil && inv.QueueName != *queueName {
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

// WorkerPoolHistory uses retained lifecycle rows, including removed replicas.
// Current residents alone cannot prove when the last scale-in/out happened.
type WorkerPoolHistory struct {
	LastAdmissionAt   time.Time
	LastTerminationAt time.Time
}

func (s *PgStore) WorkerPoolHistory(ctx context.Context, appID, deploymentID string) (WorkerPoolHistory, error) {
	appUUID, err := uuid.Parse(appID)
	if err != nil {
		return WorkerPoolHistory{}, ErrInvalidArgument
	}
	depUUID, err := uuid.Parse(deploymentID)
	if err != nil {
		return WorkerPoolHistory{}, ErrInvalidArgument
	}
	row, err := sqlc.New().WorkerPoolHistory(ctx, s.pool, sqlc.WorkerPoolHistoryParams{
		AppID: pgtype.UUID{Bytes: appUUID, Valid: true}, DeploymentID: pgtype.UUID{Bytes: depUUID, Valid: true},
	})
	if err != nil {
		return WorkerPoolHistory{}, err
	}
	history := WorkerPoolHistory{}
	if row.LastAdmissionAt.Valid {
		history.LastAdmissionAt = row.LastAdmissionAt.Time
	}
	if row.LastTerminationAt.Valid {
		history.LastTerminationAt = row.LastTerminationAt.Time
	}
	return history, nil
}

func (m *MemStore) WorkerPoolHistory(_ context.Context, appID, deploymentID string) (WorkerPoolHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	history := WorkerPoolHistory{}
	for _, ins := range m.instances {
		if ins.AppID != appID || ins.DeploymentID != deploymentID || ins.Mode != string(InstanceModeWorker) {
			continue
		}
		if ins.StartedAt.After(history.LastAdmissionAt) {
			history.LastAdmissionAt = ins.StartedAt
		}
		if ins.TerminalAt != nil && ins.TerminalAt.After(history.LastTerminationAt) {
			history.LastTerminationAt = *ins.TerminalAt
		}
	}
	return history, nil
}
