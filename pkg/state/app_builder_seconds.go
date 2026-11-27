package state

import (
	"context"
	"time"
)

// AppBuilderSecondsStore reads the builder time an app's builds used
// (ADR-20261121231500). The app usage summary's builder_seconds came from the
// app's own instances' cgroup CPU time, which is not builder time at all.
type AppBuilderSecondsStore interface {
	// AppBuilderSeconds sums the wall-clock seconds of the app's builds that
	// finished in [since, until).
	AppBuilderSeconds(ctx context.Context, appID string, since, until time.Time) (int64, error)
}

const appBuilderSecondsSQL = `select coalesce(sum(seconds), 0)::bigint
   from builder_usage
  where app_id = $1 and finished_at >= $2 and finished_at < $3`

// AppBuilderSeconds implements AppBuilderSecondsStore.
func (s *PgStore) AppBuilderSeconds(ctx context.Context, appID string, since, until time.Time) (int64, error) {
	app, err := parsePgUUID(appID)
	if err != nil {
		return 0, err
	}
	var seconds int64
	err = s.pool.QueryRow(ctx, appBuilderSecondsSQL, app, since.UTC(), until.UTC()).Scan(&seconds)
	return seconds, err
}

// AppBuilderSeconds implements AppBuilderSecondsStore.
func (m *MemStore) AppBuilderSeconds(_ context.Context, appID string, since, until time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var seconds int64
	for _, row := range m.builderUsage {
		if row.AppID == appID && !row.FinishedAt.Before(since) && row.FinishedAt.Before(until) {
			seconds += row.Seconds
		}
	}
	return seconds, nil
}
