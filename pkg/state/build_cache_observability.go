package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// BuildCacheStats is the app-scoped cache rollup used by the API. Eligible
// counts only builds for which builderd reached a cache decision, so queued
// and pre-observability rows do not dilute the rate.
type BuildCacheStats struct {
	Hits     int64
	Eligible int64
}

// validBuildCacheStatus mirrors builds_cache_status_check. Keeping the
// validation in the shared state package makes MemStore and PgStore fail in
// the same way before a malformed value reaches Postgres.
func validBuildCacheStatus(status string) bool {
	switch status {
	case "hit", "miss", "invalidated":
		return true
	default:
		return false
	}
}

func validateBuildCacheOutcome(status, keySHA256 string) error {
	if !validBuildCacheStatus(status) {
		return fmt.Errorf("state: invalid build cache status %q", status)
	}
	if keySHA256 != "" {
		if len(keySHA256) != 64 || strings.Trim(keySHA256, "0123456789abcdef") != "" {
			return errors.New("state: invalid build cache key sha256")
		}
	}
	return nil
}

// SetBuildCacheOutcome records the decision made by builderd after it has
// claimed a running build. The running-state guard prevents a late cache
// decision from writing into a build that cancellation or the reaper already
// moved to a terminal state.
func (s *PgStore) SetBuildCacheOutcome(ctx context.Context, buildID, status, keySHA256 string) error {
	if err := validateBuildCacheOutcome(status, keySHA256); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		update builds
		   set cache_status = $2,
		       cache_key_sha256 = nullif($3, '')
		 where id = $1 and status = 'running'`, buildID, status, keySHA256)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BuildCacheStatsForApp returns the trailing cache decision totals for one
// app. The caller computes the percentage so the zero-eligible case remains
// explicit and cannot be confused with a failed query.
func (s *PgStore) BuildCacheStatsForApp(ctx context.Context, appID string, since time.Time) (BuildCacheStats, error) {
	var stats BuildCacheStats
	err := s.pool.QueryRow(ctx, `
		select
			count(*) filter (where b.cache_status = 'hit'),
			count(*) filter (where b.cache_status in ('hit', 'miss', 'invalidated'))
		  from builds b
		  join deployments d on d.id = b.deployment_id
		 where d.app_id = $1
		   and b.finished_at >= $2`, appID, since).Scan(&stats.Hits, &stats.Eligible)
	return stats, err
}

// SetBuildCacheOutcome is the in-memory mirror of the Postgres CAS.
func (m *MemStore) SetBuildCacheOutcome(_ context.Context, buildID, status, keySHA256 string) error {
	if err := validateBuildCacheOutcome(status, keySHA256); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[buildID]
	if !ok || b.Status != BuildRunning {
		return ErrNotFound
	}
	b.CacheStatus = status
	b.CacheKeySHA256 = keySHA256
	m.builds[buildID] = b
	return nil
}

// BuildCacheStatsForApp is the MemStore mirror used by handler tests.
func (m *MemStore) BuildCacheStatsForApp(_ context.Context, appID string, since time.Time) (BuildCacheStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var stats BuildCacheStats
	for _, b := range m.builds {
		if b.FinishedAt.IsZero() || b.FinishedAt.Before(since) {
			continue
		}
		d, ok := m.deployments[b.DeploymentID]
		if !ok || d.AppID != appID {
			continue
		}
		switch b.CacheStatus {
		case "hit":
			stats.Hits++
			stats.Eligible++
		case "miss", "invalidated":
			stats.Eligible++
		}
	}
	return stats, nil
}
