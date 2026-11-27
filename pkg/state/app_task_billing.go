package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AppTaskBillingSlack is how long past its timeout an unfinished task is
// still billed while its runner settles it. A task that never records a
// finish stops billing there, so a crashed coordinator cannot bill forever.
const AppTaskBillingSlack = 2 * time.Minute

// appTaskBillingMaxRun is the longest an app task can run (the app_tasks
// timeout CHECK) plus the slack: the earliest start a task overlapping a
// window can have.
const appTaskBillingMaxRun = 3600*time.Second + AppTaskBillingSlack

// AppTaskBillingWindow is an app task VM that ran in a sampled window. App
// task VMs hold plan RAM like an instance but have no instance row, so meterd
// bills them from the task's own lifecycle (cost-control review 2026-11-19,
// N16).
type AppTaskBillingWindow struct {
	TaskID         string
	AppID          string
	Status         AppTaskStatus
	StartedAt      time.Time
	FinishedAt     *time.Time
	LeaseExpiresAt *time.Time
	UpdatedAt      time.Time
	TimeoutSeconds int
}

// BillableEnd is when the task stopped holding its VM, as far as billing can
// tell at now: its finish, or for a task that recorded none its last update
// once terminal, else the earliest of its lease expiry, its timeout plus the
// slack, and now.
func (w AppTaskBillingWindow) BillableEnd(now time.Time) time.Time {
	if w.FinishedAt != nil {
		return *w.FinishedAt
	}
	if w.Status.Terminal() {
		return w.UpdatedAt
	}
	end := w.StartedAt.Add(time.Duration(w.TimeoutSeconds)*time.Second + AppTaskBillingSlack)
	if w.LeaseExpiresAt != nil && w.LeaseExpiresAt.Before(end) {
		end = *w.LeaseExpiresAt
	}
	if now.Before(end) {
		end = now
	}
	return end
}

// BillableSeconds is the whole seconds the task held its VM within
// [start, end), as of now.
func (w AppTaskBillingWindow) BillableSeconds(start, end, now time.Time) int64 {
	from := w.StartedAt
	if from.Before(start) {
		from = start
	}
	to := w.BillableEnd(now)
	if to.After(end) {
		to = end
	}
	if !from.Before(to) {
		return 0
	}
	return int64(to.Sub(from) / time.Second)
}

// AppTaskBillingStore lists the app tasks whose VMs ran in a sampled window
// of at most a minute.
type AppTaskBillingStore interface {
	ListAppTasksInBillingWindow(ctx context.Context, start, end time.Time) ([]AppTaskBillingWindow, error)
}

var (
	_ AppTaskBillingStore = (*PgStore)(nil)
	_ AppTaskBillingStore = (*MemStore)(nil)
)

func validAppTaskBillingWindow(start, end time.Time) bool {
	return !start.IsZero() && start.Before(end) && end.Sub(start) <= time.Minute
}

func (s *PgStore) ListAppTasksInBillingWindow(ctx context.Context, start, end time.Time) ([]AppTaskBillingWindow, error) {
	if !validAppTaskBillingWindow(start, end) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().AppTasksInBillingWindow(ctx, s.pool, sqlc.AppTasksInBillingWindowParams{
		WindowStart: financialTimestamp(start), WindowEnd: financialTimestamp(end),
		EarliestStart: financialTimestamp(start.Add(-appTaskBillingMaxRun)),
	})
	if err != nil {
		return nil, fmt.Errorf("state: app task billing window: %w", err)
	}
	out := make([]AppTaskBillingWindow, 0, len(rows))
	for _, row := range rows {
		out = append(out, AppTaskBillingWindow{
			TaskID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), Status: AppTaskStatus(row.Status),
			StartedAt: row.StartedAt.Time.UTC(), FinishedAt: appTaskBillingTime(row.FinishedAt),
			LeaseExpiresAt: appTaskBillingTime(row.LeaseExpiresAt), UpdatedAt: row.UpdatedAt.Time.UTC(),
			TimeoutSeconds: int(row.TimeoutSeconds),
		})
	}
	return out, nil
}

func appTaskBillingTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func (m *MemStore) ListAppTasksInBillingWindow(_ context.Context, start, end time.Time) ([]AppTaskBillingWindow, error) {
	if !validAppTaskBillingWindow(start, end) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	earliest := start.Add(-appTaskBillingMaxRun)
	var out []AppTaskBillingWindow
	for _, task := range m.appTasks {
		if task.StartedAt == nil || !task.StartedAt.Before(end) || task.StartedAt.Before(earliest) ||
			(task.FinishedAt != nil && !task.FinishedAt.After(start)) {
			continue
		}
		out = append(out, AppTaskBillingWindow{
			TaskID: task.ID, AppID: task.AppID, Status: task.Status, StartedAt: task.StartedAt.UTC(),
			FinishedAt: cloneTimePtr(task.FinishedAt), LeaseExpiresAt: cloneTimePtr(task.LeaseExpiresAt),
			UpdatedAt: task.UpdatedAt.UTC(), TimeoutSeconds: task.TimeoutSeconds,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].TaskID < out[j].TaskID
	})
	return out, nil
}
