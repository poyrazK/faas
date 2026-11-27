package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// wakeBootFailedKind mirrors events.WakeBootFailed; pkg/state cannot import
// pkg/events.
const wakeBootFailedKind = "wake.boot_failed"

// FailureAlertCounter counts the failures behind the boot_failures and
// job_failures alert metrics. It is optional so narrow test stores need not
// implement it; the evaluator degrades a rule on a store without it rather
// than reporting zero failures.
type FailureAlertCounter interface {
	// CountAppBootFailures counts the app's wake.boot_failed events in
	// [since, until].
	CountAppBootFailures(ctx context.Context, appID string, since, until time.Time) (int64, error)
	// CountAccountFailedJobRuns counts the account's job runs that finished
	// failed or dead-lettered in [since, until].
	CountAccountFailedJobRuns(ctx context.Context, accountID string, since, until time.Time) (int64, error)
}

// CountAppBootFailures reads events through events_kind_at_idx: failed boots
// are rare, so the (kind, at) range is small before the app filter.
func (s *PgStore) CountAppBootFailures(ctx context.Context, appID string, since, until time.Time) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM events
		WHERE kind = $1 AND at >= $2 AND at <= $3 AND data->>'app_id' = $4`,
		wakeBootFailedKind, since, until, appID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("state: count app boot failures: %w", err)
	}
	return n, nil
}

// CountAccountFailedJobRuns reads job_runs through job_runs_failed_finished_idx.
func (s *PgStore) CountAccountFailedJobRuns(ctx context.Context, accountID string, since, until time.Time) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM job_runs
		WHERE account_id = $1::uuid AND aggregate_status IN ('failed', 'dead_letter')
		  AND finished_at >= $2 AND finished_at <= $3`,
		accountID, since, until).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("state: count account failed job runs: %w", err)
	}
	return n, nil
}

// CountAppBootFailures is the in-memory equivalent of the PgStore query.
func (m *MemStore) CountAppBootFailures(_ context.Context, appID string, since, until time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, e := range m.events {
		if e.Kind != wakeBootFailedKind || e.At.Before(since) || e.At.After(until) {
			continue
		}
		var data struct {
			AppID string `json:"app_id"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.AppID == appID {
			n++
		}
	}
	return n, nil
}

// CountAccountFailedJobRuns is the in-memory equivalent of the PgStore query.
func (m *MemStore) CountAccountFailedJobRuns(_ context.Context, accountID string, since, until time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range m.jobRuns {
		if r.AccountID != accountID || r.FinishedAt == nil || r.FinishedAt.Before(since) || r.FinishedAt.After(until) {
			continue
		}
		if r.AggregateStatus == "failed" || r.AggregateStatus == "dead_letter" {
			n++
		}
	}
	return n, nil
}
