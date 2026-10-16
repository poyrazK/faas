package state

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EnvironmentCacheScheduleActivityPruner is internal apid maintenance, not a
// customer endpoint. cutoff must be now minus the configured retention window.
type EnvironmentCacheScheduleActivityPruner interface {
	PruneEnvironmentCacheScheduleActivity(context.Context, time.Time) error
}

type EnvironmentCacheActivityExpiredError struct {
	Recovery api.EnvironmentCacheActivityRecovery
}

func (*EnvironmentCacheActivityExpiredError) Error() string {
	return "cache schedule activity cursor expired"
}
func expiredScheduleActivity(current api.EnvironmentCachePolicy, c api.EnvironmentCacheActivityCursor, floor, head int64) error {
	c.Sequence = floor
	recovery := api.EnvironmentCacheActivityRecovery{AppID: current.AppID, ProjectID: current.ProjectID, EnvironmentID: current.EnvironmentID, Scope: current.Scope, OldestCursor: api.EncodeEnvironmentCacheActivityCursor(c), RetentionSeconds: api.EnvironmentCacheActivityRetentionSeconds}
	c.Sequence = head
	recovery.RestartCursor = api.EncodeEnvironmentCacheActivityCursor(c)
	return &EnvironmentCacheActivityExpiredError{Recovery: recovery}
}

func (s *PgStore) PruneEnvironmentCacheScheduleActivity(ctx context.Context, cutoff time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	targets, err := q.LockEnvironmentCacheScheduleRetentionTargets(ctx, tx, sqlc.LockEnvironmentCacheScheduleRetentionTargetsParams{Cutoff: pgtype.Timestamptz{Time: cutoff, Valid: true}, TargetLimit: api.EnvironmentCacheActivityRetentionTargets})
	if err != nil {
		return err
	}
	for _, target := range targets {
		err = q.PruneEnvironmentCacheScheduleEvents(ctx, tx, sqlc.PruneEnvironmentCacheScheduleEventsParams{AppID: target.AppID, EnvironmentID: target.EnvironmentID, PrunedThrough: target.PrunedThrough, Cutoff: pgtype.Timestamptz{Time: cutoff, Valid: true}, BatchLimit: api.EnvironmentCacheActivityRetentionBatch})
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (m *MemStore) PruneEnvironmentCacheScheduleActivity(ctx context.Context, cutoff time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.environmentCacheScheduleEventFloors == nil {
		m.environmentCacheScheduleEventFloors = map[string]int64{}
	}
	keys := make([]string, 0, len(m.environmentCacheScheduleEvents))
	for key, events := range m.environmentCacheScheduleEvents {
		if len(events) > 0 && events[0].OccurredAt.Before(cutoff) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := m.environmentCacheScheduleEvents[keys[i]][0].OccurredAt, m.environmentCacheScheduleEvents[keys[j]][0].OccurredAt
		if a.Equal(b) {
			return keys[i] < keys[j]
		}
		return a.Before(b)
	})
	if len(keys) > api.EnvironmentCacheActivityRetentionTargets {
		keys = keys[:api.EnvironmentCacheActivityRetentionTargets]
	}
	for _, key := range keys {
		events := m.environmentCacheScheduleEvents[key]
		n := 0
		for n < len(events) && n < api.EnvironmentCacheActivityRetentionBatch && events[n].OccurredAt.Before(cutoff) {
			n++
		}
		if n > 0 {
			m.environmentCacheScheduleEventFloors[key] = events[n-1].Sequence
			// Copy so discarded payloads are no longer held by a backing array.
			m.environmentCacheScheduleEvents[key] = append([]api.EnvironmentCacheScheduleEvent(nil), events[n:]...)
		}
	}
	return nil
}
