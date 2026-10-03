package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PgStore) AppLifecycleTransitionHealth(ctx context.Context) (AppLifecycleTransitionHealth, error) {
	var health AppLifecycleTransitionHealth
	var parkOldest, wakeOldest pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		select
		    (select count(*) from app_park_transitions
		      where completed_at is null and superseded_at is null),
		    (select min(requested_at) from app_park_transitions
		      where completed_at is null and superseded_at is null),
		    (select count(*) from app_wake_transitions
		      where completed_at is null and superseded_at is null),
		    (select min(requested_at) from app_wake_transitions
		      where completed_at is null and superseded_at is null)
	`).Scan(&health.ParkPendingCount, &parkOldest, &health.WakePendingCount, &wakeOldest)
	if err != nil {
		return AppLifecycleTransitionHealth{}, fmt.Errorf("state: app lifecycle transition health: %w", err)
	}
	if parkOldest.Valid {
		at := parkOldest.Time
		health.ParkOldestPendingAt = &at
	}
	if wakeOldest.Valid {
		at := wakeOldest.Time
		health.WakeOldestPendingAt = &at
	}
	return health, nil
}
