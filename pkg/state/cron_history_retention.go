package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CronHistoryRetentionStore prunes a cron's settled schedule decisions and
// its manual-run requests once they are older than api.CronHistoryRetention.
// Each call deletes at most limit rows from each table and reports the total.
type CronHistoryRetentionStore interface {
	PruneCronHistory(ctx context.Context, before time.Time, limit int) (int, error)
}

// cronOccurrenceSettled mirrors the status list in PruneCronScheduleOccurrences.
var cronOccurrenceSettled = map[string]bool{
	"succeeded": true, "failed": true, "cancelled": true,
	"skipped_overlap": true, "missed_deadline": true, "coalesced": true,
}

func (s *PgStore) PruneCronHistory(ctx context.Context, before time.Time, limit int) (int, error) {
	if before.IsZero() || limit <= 0 || limit > api.CronHistoryPruneBatch {
		return 0, ErrInvalidArgument
	}
	beforeAt := pgtype.Timestamptz{Time: before, Valid: true}
	occurrences, err := sqlc.New().PruneCronScheduleOccurrences(ctx, s.pool, sqlc.PruneCronScheduleOccurrencesParams{
		BeforeAt: beforeAt, BatchLimit: int32(limit),
	})
	if err != nil {
		return 0, fmt.Errorf("state: prune cron schedule occurrences: %w", err)
	}
	requests, err := sqlc.New().PruneCronFireNowRequests(ctx, s.pool, sqlc.PruneCronFireNowRequestsParams{
		BeforeAt: beforeAt, BatchLimit: int32(limit),
	})
	if err != nil {
		return int(occurrences), fmt.Errorf("state: prune cron manual-run requests: %w", err)
	}
	return int(occurrences + requests), nil
}

func (m *MemStore) PruneCronHistory(ctx context.Context, before time.Time, limit int) (int, error) {
	if before.IsZero() || limit <= 0 || limit > api.CronHistoryPruneBatch {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	occurrences := make([]ScheduleOccurrence, 0)
	for _, row := range m.scheduleOccurrences {
		if row.CronID != "" && row.ScheduledFor.Before(before) && cronOccurrenceSettled[row.Status] {
			occurrences = append(occurrences, row)
		}
	}
	sort.Slice(occurrences, func(i, j int) bool {
		if !occurrences[i].ScheduledFor.Equal(occurrences[j].ScheduledFor) {
			return occurrences[i].ScheduledFor.Before(occurrences[j].ScheduledFor)
		}
		return occurrences[i].ID < occurrences[j].ID
	})
	if len(occurrences) > limit {
		occurrences = occurrences[:limit]
	}
	for _, row := range occurrences {
		m.deleteScheduleOccurrenceLocked(row.ID)
	}
	requests := make([]FireNowRequest, 0)
	for _, row := range m.fireNowRequests {
		if row.RequestedAt.Before(before) {
			requests = append(requests, row)
		}
	}
	sort.Slice(requests, func(i, j int) bool {
		if !requests[i].RequestedAt.Equal(requests[j].RequestedAt) {
			return requests[i].RequestedAt.Before(requests[j].RequestedAt)
		}
		return requests[i].ID < requests[j].ID
	})
	if len(requests) > limit {
		requests = requests[:limit]
	}
	for _, row := range requests {
		delete(m.fireNowRequests, row.ID)
	}
	return len(occurrences) + len(requests), nil
}

// deleteScheduleOccurrenceLocked removes one occurrence and clears every
// reference to it, as the ON DELETE SET NULL foreign keys do in PostgreSQL.
func (m *MemStore) deleteScheduleOccurrenceLocked(id string) {
	delete(m.scheduleOccurrences, id)
	for key, inv := range m.invocations {
		if inv.OccurrenceID == id {
			inv.OccurrenceID = ""
			m.invocations[key] = inv
		}
	}
	for key, task := range m.appTasks {
		if task.OccurrenceID == id {
			task.OccurrenceID = ""
			m.appTasks[key] = task
		}
	}
	for key, run := range m.jobRuns {
		if run.OccurrenceID == id {
			run.OccurrenceID = ""
			m.jobRuns[key] = run
		}
	}
	for key, row := range m.scheduleOccurrences {
		if row.BlockingOccurrenceID == id {
			row.BlockingOccurrenceID = ""
			m.scheduleOccurrences[key] = row
		}
	}
}

// SeedScheduleOccurrenceForTest stores an occurrence as the scheduler would
// have recorded it.
func (m *MemStore) SeedScheduleOccurrenceForTest(o ScheduleOccurrence) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduleOccurrences[o.ID] = o
}

// ScheduleOccurrenceForTest returns a stored occurrence.
func (m *MemStore) ScheduleOccurrenceForTest(id string) (ScheduleOccurrence, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.scheduleOccurrences[id]
	return o, ok
}

// SeedFireNowRequestForTest stores a manual-run request with any timestamp.
func (m *MemStore) SeedFireNowRequestForTest(r FireNowRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fireNowRequests[r.ID] = r
}

// FireNowRequestForTest reports whether a manual-run request is stored.
func (m *MemStore) FireNowRequestForTest(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.fireNowRequests[id]
	return ok
}
