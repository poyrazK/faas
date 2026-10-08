package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const ManagedRealtimeScheduleLimit = 256

var ErrManagedRealtimeScheduleLimit = errors.New("state: realtime schedule limit reached")

type managedRealtimeScheduleKey struct{ endpointID, channel, id string }
type ManagedRealtimeSchedule struct {
	Group                string            `json:"group,omitempty"`
	Conditions           json.RawMessage   `json:"conditions,omitempty"`
	OnConditionFailure   string            `json:"on_condition_failure,omitempty"`
	SkippedOccurrences   int64             `json:"skipped_occurrences"`
	SkipReason           string            `json:"skip_reason,omitempty"`
	IntervalSeconds      int               `json:"interval_seconds,omitempty"`
	MaxOccurrences       int64             `json:"max_occurrences,omitempty"`
	EndAt                *time.Time        `json:"end_at,omitempty"`
	InitialDeliverAt     time.Time         `json:"initial_deliver_at"`
	Occurrence           int64             `json:"occurrence"`
	CompletedOccurrences int64             `json:"completed_occurrences"`
	MaxAttempts          int               `json:"max_attempts"`
	BackoffSeconds       int               `json:"backoff_seconds"`
	Attempts             int64             `json:"attempts"`
	CycleAttempts        int               `json:"cycle_attempts"`
	NextAttemptAt        *time.Time        `json:"next_attempt_at,omitempty"`
	LastAttemptAt        *time.Time        `json:"last_attempt_at,omitempty"`
	EndpointID           string            `json:"-"`
	Channel              string            `json:"channel"`
	ID                   string            `json:"schedule_id"`
	Data                 []byte            `json:"-"`
	Binary               bool              `json:"binary"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	DeliverAt            time.Time         `json:"deliver_at"`
	Version              int64             `json:"version"`
	Status               string            `json:"status"`
	Sequence             int64             `json:"sequence,omitempty"`
	LastError            string            `json:"last_error,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}
type ManagedRealtimeScheduleStore interface {
	PutManagedRealtimeSchedule(context.Context, ManagedRealtimeSchedule) (ManagedRealtimeSchedule, error)
	ListManagedRealtimeSchedules(context.Context, string, string) ([]ManagedRealtimeSchedule, error)
	UpdateManagedRealtimeSchedule(context.Context, string, string, string, int64, *time.Time) (ManagedRealtimeSchedule, error)
	ListDueManagedRealtimeSchedules(context.Context, int) ([]ManagedRealtimeSchedule, error)
	PublishManagedRealtimeSchedule(context.Context, ManagedRealtimeSchedule) (ManagedRealtimeChannelMessage, error)
	FailManagedRealtimeSchedule(context.Context, ManagedRealtimeSchedule, string) error
}

func normalizeRealtimeRetryPolicy(row *ManagedRealtimeSchedule) {
	if len(row.Conditions) > 0 && row.OnConditionFailure == "" {
		row.OnConditionFailure = "retry"
	}
	if row.EndAt != nil {
		end := row.EndAt.UTC().Truncate(time.Microsecond)
		row.EndAt = &end
	}
	if row.MaxAttempts == 0 {
		row.MaxAttempts = 1
	}
	if row.BackoffSeconds == 0 {
		row.BackoffSeconds = 5
	}
}
func realtimeScheduleDueAt(row ManagedRealtimeSchedule) time.Time {
	if row.NextAttemptAt != nil {
		return *row.NextAttemptAt
	}
	return row.DeliverAt
}
func recordRealtimeScheduleFailure(row ManagedRealtimeSchedule, code string, now time.Time) ManagedRealtimeSchedule {
	row.Attempts++
	row.CycleAttempts++
	row.LastError = code
	row.LastAttemptAt = &now
	row.UpdatedAt = now
	row.NextAttemptAt = nil
	if row.CycleAttempts < row.MaxAttempts && row.Attempts < managedRealtimeMaxEntityVersion && row.Version < managedRealtimeMaxEntityVersion-1 {
		delay := time.Duration(row.BackoffSeconds) * time.Second
		for i := 1; i < row.CycleAttempts && delay < time.Hour; i++ {
			delay *= 2
		}
		if delay > time.Hour {
			delay = time.Hour
		}
		next := now.Add(delay).Truncate(time.Microsecond)
		row.NextAttemptAt = &next
		row.Status = "pending"
	} else {
		row.Status = "failed"
	}
	if row.Version < managedRealtimeMaxEntityVersion {
		row.Version++
	}
	return row
}
func validateRealtimeSchedule(row ManagedRealtimeSchedule) error {
	if row.Group != "" && !reducerKeyValid(row.Group) {
		return ErrManagedRealtimeHistoryInvalid
	}
	if _, err := decodeScheduleConditions(row.Conditions); err != nil {
		return err
	}
	if (row.OnConditionFailure != "" && row.OnConditionFailure != "skip" && row.OnConditionFailure != "retry") || (len(row.Conditions) == 0 && row.OnConditionFailure != "") {
		return ErrManagedRealtimeHistoryInvalid
	}
	if row.IntervalSeconds > 0 {
		if len(row.Metadata) > 6 {
			return ErrManagedRealtimeHistoryInvalid
		}
		if _, ok := row.Metadata["schedule_id"]; ok {
			return ErrManagedRealtimeHistoryInvalid
		}
		if _, ok := row.Metadata["schedule_occurrence"]; ok {
			return ErrManagedRealtimeHistoryInvalid
		}
	}
	if row.IntervalSeconds != 0 && (row.IntervalSeconds < 5 || row.IntervalSeconds > 2592000) {
		return ErrManagedRealtimeHistoryInvalid
	}
	if row.MaxOccurrences < 0 || row.MaxOccurrences > 1000000 || (row.IntervalSeconds == 0 && (row.MaxOccurrences != 0 || row.EndAt != nil)) {
		return ErrManagedRealtimeHistoryInvalid
	}
	if row.EndAt != nil && (row.EndAt.Before(row.DeliverAt) || row.EndAt.After(row.DeliverAt.Add(365*24*time.Hour))) {
		return ErrManagedRealtimeHistoryInvalid
	}
	if row.MaxAttempts < 1 || row.MaxAttempts > 10 || row.BackoffSeconds < 5 || row.BackoffSeconds > 3600 {
		return ErrManagedRealtimeHistoryInvalid
	}
	if validateManagedRealtimeHistoryAppend(row.EndpointID, row.Channel, row.Data, row.ID) != nil || !reducerKeyValid(row.ID) || api.ValidateRealtimeMetadata(row.Metadata) != nil {
		return ErrManagedRealtimeHistoryInvalid
	}
	return nil
}
func validRealtimeDeliverAt(deadline, now time.Time) bool {
	return deadline.After(now) && !deadline.After(now.Add(30*24*time.Hour))
}
func cloneRealtimeSchedule(row ManagedRealtimeSchedule) ManagedRealtimeSchedule {
	row.Data = append([]byte(nil), row.Data...)
	row.Conditions = append(json.RawMessage(nil), row.Conditions...)
	row.Metadata = cloneRealtimeMetadata(row.Metadata)
	if row.EndAt != nil {
		end := *row.EndAt
		row.EndAt = &end
	}
	if row.NextAttemptAt != nil {
		value := *row.NextAttemptAt
		row.NextAttemptAt = &value
	}
	if row.LastAttemptAt != nil {
		value := *row.LastAttemptAt
		row.LastAttemptAt = &value
	}
	return row
}
func scheduleMatches(row, candidate ManagedRealtimeSchedule, now time.Time) bool {
	return row.Status == "pending" && row.Version == candidate.Version && row.Occurrence == candidate.Occurrence && row.Version < managedRealtimeMaxEntityVersion && row.Attempts < managedRealtimeMaxEntityVersion && row.CycleAttempts < row.MaxAttempts && row.DeliverAt.Equal(candidate.DeliverAt) && !realtimeScheduleDueAt(row).After(now) && row.Binary == candidate.Binary && bytes.Equal(row.Data, candidate.Data) && equalRealtimeMetadata(row.Metadata, candidate.Metadata)
}
func scheduleKey(row ManagedRealtimeSchedule) managedRealtimeScheduleKey {
	return managedRealtimeScheduleKey{row.EndpointID, row.Channel, row.ID}
}
func (m *MemStore) trimRealtimeSchedulesLocked(now time.Time) {
	for key, row := range m.managedRealtimeSchedules {
		if row.Status != "pending" && row.Status != "paused" && row.UpdatedAt.Before(now.Add(-24*time.Hour)) {
			delete(m.managedRealtimeSchedules, key)
			delete(m.managedRealtimeScheduleHistory, key)
		}
	}
}
func (m *MemStore) PutManagedRealtimeSchedule(ctx context.Context, row ManagedRealtimeSchedule) (ManagedRealtimeSchedule, error) {
	normalizeRealtimeRetryPolicy(&row)
	row.DeliverAt = row.DeliverAt.UTC().Truncate(time.Microsecond)
	if err := validateRealtimeSchedule(row); err != nil {
		return row, err
	}
	if err := ctx.Err(); err != nil {
		return row, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[row.EndpointID]; !ok {
		return row, ErrNotFound
	}
	now := time.Now().UTC()
	m.trimRealtimeSchedulesLocked(now)
	key := scheduleKey(row)
	if old, ok := m.managedRealtimeSchedules[key]; ok {
		if sameRealtimeScheduleCreation(old, row) {
			return cloneRealtimeSchedule(old), nil
		}
		return row, ErrConflict
	}
	if !validRealtimeDeliverAt(row.DeliverAt, now) {
		return row, ErrManagedRealtimeHistoryInvalid
	}
	count := 0
	for k := range m.managedRealtimeSchedules {
		if k.endpointID == row.EndpointID {
			count++
		}
	}
	if count >= ManagedRealtimeScheduleLimit {
		return row, ErrManagedRealtimeScheduleLimit
	}
	row.SkippedOccurrences = 0
	row.SkipReason = ""
	row.InitialDeliverAt = row.DeliverAt
	row.Occurrence = 1
	row.CompletedOccurrences = 0
	row.Version = 1
	row.Status = "pending"
	row.Sequence = 0
	row.LastError = ""
	row.Attempts = 0
	row.CycleAttempts = 0
	row.NextAttemptAt = nil
	row.LastAttemptAt = nil
	row.CreatedAt = now
	row.UpdatedAt = now
	if m.managedRealtimeSchedules == nil {
		m.managedRealtimeSchedules = make(map[managedRealtimeScheduleKey]ManagedRealtimeSchedule)
	}
	m.managedRealtimeSchedules[key] = cloneRealtimeSchedule(row)
	m.recordRealtimeScheduleHistoryLocked(row, "created")
	return cloneRealtimeSchedule(row), nil
}
func (m *MemStore) ListManagedRealtimeSchedules(ctx context.Context, ep, ch string) ([]ManagedRealtimeSchedule, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trimRealtimeSchedulesLocked(time.Now().UTC())
	out := make([]ManagedRealtimeSchedule, 0)
	for key, row := range m.managedRealtimeSchedules {
		if key.endpointID == ep && key.channel == ch {
			out = append(out, cloneRealtimeSchedule(row))
		}
	}
	sortRealtimeSchedules(out)
	return out, nil
}
func sortRealtimeSchedules(rows []ManagedRealtimeSchedule) {
	sort.Slice(rows, func(i, j int) bool {
		if realtimeScheduleDueAt(rows[i]).Equal(realtimeScheduleDueAt(rows[j])) {
			if rows[i].EndpointID != rows[j].EndpointID {
				return rows[i].EndpointID < rows[j].EndpointID
			}
			if rows[i].Channel != rows[j].Channel {
				return rows[i].Channel < rows[j].Channel
			}
			return rows[i].ID < rows[j].ID
		}
		return realtimeScheduleDueAt(rows[i]).Before(realtimeScheduleDueAt(rows[j]))
	})
}
func (m *MemStore) UpdateManagedRealtimeSchedule(ctx context.Context, ep, ch, id string, version int64, deadline *time.Time) (ManagedRealtimeSchedule, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || !reducerKeyValid(id) || version < 1 {
		return ManagedRealtimeSchedule{}, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeSchedule{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := managedRealtimeScheduleKey{ep, ch, id}
	row, ok := m.managedRealtimeSchedules[key]
	if !ok {
		return row, ErrNotFound
	}
	if deadline == nil && row.Status == "canceled" {
		return cloneRealtimeSchedule(row), nil
	}
	if (row.Status != "pending" && !(deadline == nil && row.Status == "paused")) || row.Version != version || row.Version >= managedRealtimeMaxEntityVersion-1 {
		return row, ErrConflict
	}
	now := time.Now().UTC()
	if deadline != nil {
		if row.EndAt != nil && deadline.After(*row.EndAt) {
			return row, ErrManagedRealtimeHistoryInvalid
		}
		if !validRealtimeDeliverAt(*deadline, now) {
			return row, ErrManagedRealtimeHistoryInvalid
		}
		row.DeliverAt = deadline.UTC().Truncate(time.Microsecond)
	} else {
		row.Status = "canceled"
	}
	row.NextAttemptAt = nil
	row.Version++
	row.UpdatedAt = now
	m.managedRealtimeSchedules[key] = row
	kind := "rescheduled"
	if deadline == nil {
		kind = "canceled"
	}
	m.recordRealtimeScheduleHistoryLocked(row, kind)
	return cloneRealtimeSchedule(row), nil
}
func (m *MemStore) ListDueManagedRealtimeSchedules(ctx context.Context, limit int) ([]ManagedRealtimeSchedule, error) {
	if limit < 1 || limit > 256 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.trimRealtimeSchedulesLocked(now)
	out := make([]ManagedRealtimeSchedule, 0, limit)
	for _, row := range m.managedRealtimeSchedules {
		if row.Status == "pending" && !realtimeScheduleDueAt(row).After(now) {
			// Bound candidate memory while prioritizing older deadlines.
			out = append(out, row)
			sortRealtimeSchedules(out)
			if len(out) > limit {
				out = out[:limit]
			}
		}
	}
	for i := range out {
		out[i] = cloneRealtimeSchedule(out[i])
	}
	return out, nil
}
func (m *MemStore) PublishManagedRealtimeSchedule(ctx context.Context, row ManagedRealtimeSchedule) (ManagedRealtimeChannelMessage, error) {
	return m.appendManagedRealtimeChannel(ctx, row.EndpointID, row.Channel, row.Data, row.Binary, "", scheduledRealtimeMetadata(row), nil, nil, &row)
}
func (m *MemStore) FailManagedRealtimeSchedule(ctx context.Context, candidate ManagedRealtimeSchedule, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if code == "" || len(code) > 128 || strings.ContainsAny(code, "\r\n") {
		return ErrManagedRealtimeHistoryInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := scheduleKey(candidate)
	row, ok := m.managedRealtimeSchedules[key]
	if ok && scheduleMatches(row, candidate, time.Now().UTC()) {
		row = recordRealtimeScheduleFailure(row, code, time.Now().UTC())
		m.managedRealtimeSchedules[key] = row
		m.recordRealtimeScheduleHistoryLocked(row, "attempt_failed")
	}
	return nil
}
