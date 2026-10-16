package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EnvironmentCacheScheduleActivityCheckpointReader interface {
	GetEnvironmentCacheScheduleActivityCheckpoint(context.Context, string, string, string) (api.EnvironmentCacheActivityRecovery, error)
}

func activityCheckpoint(current api.EnvironmentCachePolicy, account string, floor, head int64) api.EnvironmentCacheActivityRecovery {
	c := api.EnvironmentCacheActivityCursor{AccountID: account, AppID: current.AppID, ProjectID: current.ProjectID, EnvironmentID: current.EnvironmentID, Sequence: floor}
	out := api.EnvironmentCacheActivityRecovery{AppID: current.AppID, ProjectID: current.ProjectID, EnvironmentID: current.EnvironmentID, Scope: current.Scope, OldestCursor: api.EncodeEnvironmentCacheActivityCursor(c), RetentionSeconds: api.EnvironmentCacheActivityRetentionSeconds}
	c.Sequence = head
	out.RestartCursor = api.EncodeEnvironmentCacheActivityCursor(c)
	return out
}

func (s *PgStore) GetEnvironmentCacheScheduleActivityCheckpoint(ctx context.Context, account, app, scope string) (api.EnvironmentCacheActivityRecovery, error) {
	var out api.EnvironmentCacheActivityRecovery
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return out, err
	}
	bounds, err := sqlc.New().ReadEnvironmentCacheScheduleEventBounds(ctx, tx, sqlc.ReadEnvironmentCacheScheduleEventBoundsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(current.EnvironmentID)})
	if err != nil {
		return out, err
	}
	out = activityCheckpoint(current, account, bounds.Floor, bounds.Head)
	return out, tx.Commit(ctx)
}
func (m *MemStore) GetEnvironmentCacheScheduleActivityCheckpoint(_ context.Context, account, app, scope string) (api.EnvironmentCacheActivityRecovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return api.EnvironmentCacheActivityRecovery{}, err
	}
	key := account + "/" + app + "/" + current.EnvironmentID
	floor := m.environmentCacheScheduleEventFloors[key]
	return activityCheckpoint(current, account, floor, floor+int64(len(m.environmentCacheScheduleEvents[key]))), nil
}
