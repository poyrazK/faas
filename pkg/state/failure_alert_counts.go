package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Mirrors of pkg/events values; pkg/state cannot import pkg/events.
const (
	wakeBootFailedKind = "wake.boot_failed"
	// vmmBootFailedReason is the only BootFailed reason the boot_failures
	// metric counts: the VM failed to start. record_runtime_failed is schedd
	// failing to publish runtime state, a platform cause.
	vmmBootFailedReason = "vmm_boot_failed"
)

// FailureAlertCounter counts the failures behind the boot_failures and
// job_failures alert metrics. It is optional so narrow test stores need not
// implement it; the evaluator degrades a rule on a store without it rather
// than reporting zero failures.
type FailureAlertCounter interface {
	// CountAppBootFailures counts the app's failed VM boots in [since,
	// until], leaving out mirror-mode boots of a candidate deployment.
	CountAppBootFailures(ctx context.Context, appID string, since, until time.Time) (int64, error)
	// CountAccountFailedJobRuns counts the account's job runs that finished
	// failed or dead-lettered in [since, until].
	CountAccountFailedJobRuns(ctx context.Context, accountID string, since, until time.Time) (int64, error)
}

// CountAppBootFailures runs the sqlc CountAppBootFailures query.
func (s *PgStore) CountAppBootFailures(ctx context.Context, appID string, since, until time.Time) (int64, error) {
	n, err := sqlc.New().CountAppBootFailures(ctx, s.pool, sqlc.CountAppBootFailuresParams{
		AppID: appID,
		Since: pgtype.Timestamptz{Time: since, Valid: true},
		Until: pgtype.Timestamptz{Time: until, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("state: count app boot failures: %w", err)
	}
	return n, nil
}

// CountAccountFailedJobRuns runs the sqlc CountAccountFailedJobRuns query.
func (s *PgStore) CountAccountFailedJobRuns(ctx context.Context, accountID string, since, until time.Time) (int64, error) {
	var account pgtype.UUID
	if err := account.Scan(accountID); err != nil {
		return 0, fmt.Errorf("state: count account failed job runs: account id: %w", err)
	}
	n, err := sqlc.New().CountAccountFailedJobRuns(ctx, s.pool, sqlc.CountAccountFailedJobRunsParams{
		AccountID: account,
		Since:     pgtype.Timestamptz{Time: since, Valid: true},
		Until:     pgtype.Timestamptz{Time: until, Valid: true},
	})
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
			AppID  string `json:"app_id"`
			Reason string `json:"reason"`
			Mirror *bool  `json:"mirror"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.AppID == appID && data.Reason == vmmBootFailedReason && data.Mirror == nil {
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
