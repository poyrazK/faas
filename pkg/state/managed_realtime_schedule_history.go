package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"time"
)

const ManagedRealtimeScheduleHistoryLimit = 128

type ManagedRealtimeScheduleHistoryEvent struct {
	SkippedOccurrences   int64      `json:"skipped_occurrences"`
	SkipReason           string     `json:"skip_reason,omitempty"`
	Occurrence           int64      `json:"occurrence"`
	CompletedOccurrences int64      `json:"completed_occurrences"`
	Version              int64      `json:"version"`
	Event                string     `json:"event"`
	Status               string     `json:"status"`
	Attempts             int64      `json:"attempts"`
	CycleAttempts        int        `json:"cycle_attempts"`
	DeliverAt            time.Time  `json:"deliver_at"`
	NextAttemptAt        *time.Time `json:"next_attempt_at,omitempty"`
	FailureCode          string     `json:"failure_code,omitempty"`
	Sequence             int64      `json:"sequence,omitempty"`
	OccurredAt           time.Time  `json:"occurred_at"`
}
type ManagedRealtimeScheduleHistory struct {
	ScheduleID       string                                `json:"schedule_id"`
	Channel          string                                `json:"channel"`
	OldestVersion    int64                                 `json:"oldest_version"`
	LatestVersion    int64                                 `json:"latest_version"`
	HistoryTruncated bool                                  `json:"history_truncated"`
	HasMore          bool                                  `json:"has_more"`
	Events           []ManagedRealtimeScheduleHistoryEvent `json:"events"`
}
type ManagedRealtimeScheduleHistoryStore interface {
	ReadManagedRealtimeScheduleHistory(context.Context, string, string, string, int64, int) (ManagedRealtimeScheduleHistory, error)
}

func realtimeScheduleHistoryEvent(row ManagedRealtimeSchedule, kind string) ManagedRealtimeScheduleHistoryEvent {
	event := ManagedRealtimeScheduleHistoryEvent{SkippedOccurrences: row.SkippedOccurrences, Occurrence: row.Occurrence, CompletedOccurrences: row.CompletedOccurrences, Version: row.Version, Event: kind, Status: row.Status, Attempts: row.Attempts, CycleAttempts: row.CycleAttempts, DeliverAt: row.DeliverAt, Sequence: row.Sequence, OccurredAt: row.UpdatedAt}
	if row.NextAttemptAt != nil {
		deadline := *row.NextAttemptAt
		event.NextAttemptAt = &deadline
	}
	if kind == "skipped" {
		event.SkipReason = row.SkipReason
		event.FailureCode = "realtime_schedule_condition_failed"
	}
	if kind != "published" && kind != "baseline" {
		event.Sequence = 0
	}
	if kind == "attempt_failed" {
		event.FailureCode = row.LastError
	}
	return event
}
func (m *MemStore) recordRealtimeScheduleHistoryLocked(row ManagedRealtimeSchedule, kind string) {
	if m.managedRealtimeScheduleHistory == nil {
		m.managedRealtimeScheduleHistory = make(map[managedRealtimeScheduleKey][]ManagedRealtimeScheduleHistoryEvent)
	}
	key := scheduleKey(row)
	events := append(m.managedRealtimeScheduleHistory[key], realtimeScheduleHistoryEvent(row, kind))
	if len(events) > ManagedRealtimeScheduleHistoryLimit {
		copy(events, events[len(events)-ManagedRealtimeScheduleHistoryLimit:])
		events[ManagedRealtimeScheduleHistoryLimit] = ManagedRealtimeScheduleHistoryEvent{}
		events = events[:ManagedRealtimeScheduleHistoryLimit]
	}
	m.managedRealtimeScheduleHistory[key] = events
	m.enqueueRealtimeScheduleCompletionLocked(row, kind)
}
func recordRealtimeScheduleHistoryPG(ctx context.Context, tx pgx.Tx, row ManagedRealtimeSchedule, kind string) error {
	event := realtimeScheduleHistoryEvent(row, kind)
	_, err := tx.Exec(ctx, `insert into managed_realtime_schedule_history(endpoint_id,channel,schedule_id,version,event,status,attempts,cycle_attempts,deliver_at,next_attempt_at,failure_code,sequence,occurred_at,occurrence,completed_occurrences,skipped_occurrences,skip_reason) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, row.EndpointID, row.Channel, row.ID, event.Version, event.Event, event.Status, event.Attempts, event.CycleAttempts, event.DeliverAt, event.NextAttemptAt, event.FailureCode, event.Sequence, event.OccurredAt, event.Occurrence, event.CompletedOccurrences, event.SkippedOccurrences, event.SkipReason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `delete from managed_realtime_schedule_history where endpoint_id=$1 and channel=$2 and schedule_id=$3 and version < (select coalesce(min(version),0) from (select version from managed_realtime_schedule_history where endpoint_id=$1 and channel=$2 and schedule_id=$3 order by version desc limit 128) retained)`, row.EndpointID, row.Channel, row.ID)
	if err != nil {
		return err
	}
	return enqueueRealtimeScheduleCompletionPG(ctx, tx, row, kind)
}
func scheduleHistoryPage(row ManagedRealtimeSchedule, events []ManagedRealtimeScheduleHistoryEvent, after int64, limit int) ManagedRealtimeScheduleHistory {
	page := ManagedRealtimeScheduleHistory{ScheduleID: row.ID, Channel: row.Channel, LatestVersion: row.Version, Events: make([]ManagedRealtimeScheduleHistoryEvent, 0)}
	if len(events) > 0 {
		page.OldestVersion = events[0].Version
		page.HistoryTruncated = page.OldestVersion > 1 || events[0].Event == "baseline"
	}
	for _, event := range events {
		if event.Version <= after {
			continue
		}
		if len(page.Events) == limit {
			page.HasMore = true
			break
		}
		if event.NextAttemptAt != nil {
			deadline := *event.NextAttemptAt
			event.NextAttemptAt = &deadline
		}
		page.Events = append(page.Events, event)
	}
	return page
}
func validateScheduleHistoryRequest(ep, ch, id string, after int64, limit int) error {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || !reducerKeyValid(id) || after < 0 || limit < 1 || limit > 100 {
		return ErrManagedRealtimeHistoryInvalid
	}
	return nil
}
func (m *MemStore) ReadManagedRealtimeScheduleHistory(ctx context.Context, ep, ch, id string, after int64, limit int) (ManagedRealtimeScheduleHistory, error) {
	if err := validateScheduleHistoryRequest(ep, ch, id, after, limit); err != nil {
		return ManagedRealtimeScheduleHistory{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeScheduleHistory{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trimRealtimeSchedulesLocked(time.Now().UTC())
	key := managedRealtimeScheduleKey{ep, ch, id}
	row, ok := m.managedRealtimeSchedules[key]
	if !ok {
		return ManagedRealtimeScheduleHistory{}, ErrNotFound
	}
	return scheduleHistoryPage(row, m.managedRealtimeScheduleHistory[key], after, limit), nil
}
func (s *PgStore) ReadManagedRealtimeScheduleHistory(ctx context.Context, ep, ch, id string, after int64, limit int) (ManagedRealtimeScheduleHistory, error) {
	if err := validateScheduleHistoryRequest(ep, ch, id, after, limit); err != nil {
		return ManagedRealtimeScheduleHistory{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ManagedRealtimeScheduleHistory{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := readRealtimeSchedulePG(ctx, tx, ep, ch, id, false)
	if err != nil {
		return ManagedRealtimeScheduleHistory{}, err
	}
	rows, err := tx.Query(ctx, `select version,event,status,attempts,cycle_attempts,deliver_at,next_attempt_at,failure_code,sequence,occurred_at,occurrence,completed_occurrences,skipped_occurrences,skip_reason from managed_realtime_schedule_history where endpoint_id=$1 and channel=$2 and schedule_id=$3 order by version limit 128`, ep, ch, id)
	if err != nil {
		return ManagedRealtimeScheduleHistory{}, err
	}
	events := make([]ManagedRealtimeScheduleHistoryEvent, 0)
	for rows.Next() {
		var event ManagedRealtimeScheduleHistoryEvent
		if err = rows.Scan(&event.Version, &event.Event, &event.Status, &event.Attempts, &event.CycleAttempts, &event.DeliverAt, &event.NextAttemptAt, &event.FailureCode, &event.Sequence, &event.OccurredAt, &event.Occurrence, &event.CompletedOccurrences, &event.SkippedOccurrences, &event.SkipReason); err != nil {
			rows.Close()
			return ManagedRealtimeScheduleHistory{}, err
		}
		events = append(events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ManagedRealtimeScheduleHistory{}, err
	}
	page := scheduleHistoryPage(row, events, after, limit)
	return page, tx.Commit(ctx)
}
