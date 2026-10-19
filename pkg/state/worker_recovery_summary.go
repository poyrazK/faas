package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// WorkerRecoverySummary is an owner-scoped aggregate with no customer labels.
// Counts come from fresh scheduler observations; absent/stale plans are unknown.
type WorkerRecoverySummary struct {
	Apps, Unobserved, Overdue                     int64
	Desired, Running, Starting, Draining, Missing int64
}

func (s *PgStore) WorkerRecoverySummary(ctx context.Context, owner string, now time.Time) (WorkerRecoverySummary, error) {
	r, err := sqlc.New().WorkerRecoverySummary(ctx, s.pool, sqlc.WorkerRecoverySummaryParams{
		OwnerNodeID:   owner,
		OverdueBefore: workerRecoveryTime(now.Add(-time.Duration(api.WorkerRecoveryOverdueGraceSeconds) * time.Second)),
		FreshAfter:    workerRecoveryTime(now.Add(-time.Duration(api.WorkerRecoveryObservationFreshnessSeconds) * time.Second)),
	})
	if err != nil {
		return WorkerRecoverySummary{}, fmt.Errorf("state: summarize worker recovery: %w", err)
	}
	return WorkerRecoverySummary{Apps: r.Apps, Unobserved: r.Unobserved, Overdue: r.Overdue,
		Desired: r.Desired, Running: r.Running, Starting: r.Starting, Draining: r.Draining, Missing: r.Missing}, nil
}

func (m *MemStore) WorkerRecoverySummary(_ context.Context, owner string, now time.Time) (WorkerRecoverySummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var summary WorkerRecoverySummary
	freshAfter := now.Add(-time.Duration(api.WorkerRecoveryObservationFreshnessSeconds) * time.Second)
	overdueBefore := now.Add(-time.Duration(api.WorkerRecoveryOverdueGraceSeconds) * time.Second)
	for id, app := range m.apps {
		if app.Status != AppActive || !m.workerRecoveryCandidateLocked(app) || (owner != "" && app.NodeID != "" && app.NodeID != owner) {
			continue
		}
		if account, ok := m.accounts[app.AccountID]; !ok || !account.Active() {
			continue
		}
		summary.Apps++
		r, exists := m.workerRecovery[id]
		due := r.NextAttemptAt
		if !exists {
			due = app.CreatedAt
		}
		if r.LeaseUntil.After(due) {
			due = r.LeaseUntil
		}
		if due.Before(overdueBefore) {
			summary.Overdue++
		}
		unknown := len(r.Pools) == 0
		for _, pool := range r.Pools {
			if pool.Revision != r.Revision || pool.ObservedAt.Before(freshAfter) {
				unknown = true
				continue
			}
			summary.Desired += int64(pool.Desired)
			summary.Running += int64(pool.Running)
			summary.Starting += int64(pool.Starting)
			summary.Draining += int64(pool.Draining)
			summary.Missing += int64(max(0, pool.Desired-pool.Running))
		}
		if unknown {
			summary.Unobserved++
		}
	}
	return summary, nil
}
