package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func (m *MemStore) CreateScheduledCronInvocationOccurrence(_ context.Context, cronID string, expectedLastFiredAt *time.Time, evaluatedAt time.Time, options CronScheduledOccurrenceOptions, invocation Invocation) (Invocation, ScheduleOccurrence, bool, error) {
	if cronID == "" || evaluatedAt.IsZero() || options.ScheduleRevision <= 0 {
		return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
	}
	evaluatedAt = evaluatedAt.UTC()
	scheduledFor := options.ScheduledFor.UTC()
	if options.ScheduledFor.IsZero() {
		scheduledFor = evaluatedAt
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cron, ok := m.crons[cronID]
	if !ok || !cron.Enabled || cron.SuspendedReason != "" || len(cron.Command) != 0 ||
		!sameTimePointer(nonZeroTimePtr(cron.LastFiredAt), expectedLastFiredAt) ||
		cron.ScheduleRevision != options.ScheduleRevision ||
		(expectedLastFiredAt != nil && !scheduledFor.After(*expectedLastFiredAt)) {
		return Invocation{}, ScheduleOccurrence{}, false, nil
	}
	app, ok := m.apps[cron.AppID]
	if !ok || app.Status == AppDeleted {
		return Invocation{}, ScheduleOccurrence{}, false, ErrNotFound
	}
	if invocation.AppID != "" && invocation.AppID != app.ID {
		return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
	}
	if invocation.AccountID != "" && invocation.AccountID != app.AccountID {
		return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
	}
	invocation.AppID, invocation.AccountID = app.ID, app.AccountID
	invocation.Source = InvocationCron
	invocation.FailureRules = workpolicy.Clone(cron.FailureRules)
	cronIDCopy := cronID
	invocation.CronID = &cronIDCopy
	if err := m.platformTenantInvocationAllowedLocked(invocation); err != nil {
		return Invocation{}, ScheduleOccurrence{}, false, err
	}
	policy := effectiveCronSchedulePolicy(cron)
	deadline := policy.Deadline(scheduledFor)
	status, reason, blocker := "queued", "", ""
	if options.Disposition != "" {
		if options.Disposition != "coalesced" && options.Disposition != "missed_deadline" {
			return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
		}
		status, reason = options.Disposition, options.Reason
	} else if workpolicy.DeadlineMissed(deadline, evaluatedAt) {
		status, reason = "missed_deadline", "start deadline expired before the scheduler could dispatch the occurrence"
	}
	var replacedPending []Invocation
	if status == "queued" && policy.Overlap != "allow" {
		active := make([]Invocation, 0, 2)
		for _, candidate := range m.invocations {
			if candidate.CronID == nil || *candidate.CronID != cronID || candidate.Source != InvocationCron ||
				(candidate.State != InvocationPending && candidate.State != InvocationDispatching) {
				continue
			}
			active = append(active, candidate)
		}
		sort.Slice(active, func(i, j int) bool {
			if active[i].CreatedAt.Equal(active[j].CreatedAt) {
				return active[i].ID < active[j].ID
			}
			return active[i].CreatedAt.Before(active[j].CreatedAt)
		})
		if len(active) > 0 {
			if policy.Overlap == "skip" {
				status, reason, blocker = "skipped_overlap", "an earlier invocation for this cron is still active", active[0].OccurrenceID
			} else {
				for _, candidate := range active {
					if candidate.State != InvocationPending || candidate.OccurrenceID == "" || candidate.ReceivedAt != nil {
						return Invocation{}, ScheduleOccurrence{}, false, nil
					}
				}
				replacedPending = active
			}
		}
	}
	if invocation.State == "" {
		invocation.State = InvocationPending
	}
	if invocation.State != InvocationPending {
		return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
	}
	invocation.OccurrenceID = ""
	invocation.StartDeadlineAt = cloneTimePtr(deadline)
	invocation.DueAt = scheduledFor
	invocation.ScheduledAt = cloneTimePtr(&scheduledFor)
	if invocation.CreatedAt.IsZero() {
		invocation.CreatedAt = evaluatedAt
	}
	if invocation.ID == "" {
		invocation.ID = newID()
	}
	if _, exists := m.invocations[invocation.ID]; exists {
		return Invocation{}, ScheduleOccurrence{}, false, ErrConflict
	}
	occurrence := ScheduleOccurrence{
		ID: newUUIDString(), AccountID: app.AccountID, CronID: cronID,
		ScheduleRevision: cron.ScheduleRevision, ScheduledFor: scheduledFor,
		StartDeadlineAt: cloneTimePtr(deadline), SchedulePolicy: *workpolicy.Clone(policy),
		Status: status, Reason: reason, BlockingOccurrenceID: blocker,
		CreatedAt: evaluatedAt, UpdatedAt: evaluatedAt,
	}
	if status == "queued" {
		invocation.OccurrenceID = occurrence.ID
	}
	cron.LastFiredAt = scheduledFor
	m.crons[cronID] = cron
	m.scheduleOccurrences[occurrence.ID] = occurrence
	for _, old := range replacedPending {
		old.State = InvocationCancelled
		old.Outcome = nil
		old.CompletedAt = cloneTimePtr(&evaluatedAt)
		old.QuotaReserved = false
		old.LastError = "replaced by a newer scheduled occurrence"
		m.invocations[old.ID] = old
		m.syncInvocationOccurrenceLocked(old, evaluatedAt)
		if oldOccurrence, exists := m.scheduleOccurrences[old.OccurrenceID]; exists {
			oldOccurrence.Reason = "cancelled before dispatch by a newer scheduled occurrence"
			oldOccurrence.UpdatedAt = evaluatedAt
			m.scheduleOccurrences[old.OccurrenceID] = oldOccurrence
		}
	}
	if status != "queued" {
		return Invocation{}, cloneScheduleOccurrence(occurrence), false, nil
	}
	m.invocations[invocation.ID] = invocation
	occurrence.InvocationID = invocation.ID
	m.scheduleOccurrences[occurrence.ID] = occurrence
	return invocation, cloneScheduleOccurrence(occurrence), true, nil
}

func outcomePtr(outcome InvocationOutcome) *InvocationOutcome {
	return &outcome
}

func (m *MemStore) ExpireUnstartedScheduledCronInvocations(_ context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 64
	}
	now = now.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0)
	for id, inv := range m.invocations {
		if inv.State == InvocationPending && inv.Source == InvocationCron && inv.OccurrenceID != "" &&
			inv.StartDeadlineAt != nil && inv.StartDeadlineAt.Before(now) && inv.ReceivedAt == nil {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		return m.invocations[ids[i]].StartDeadlineAt.Before(*m.invocations[ids[j]].StartDeadlineAt)
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	for _, id := range ids {
		inv := m.invocations[id]
		inv.State = InvocationFailed
		inv.Outcome = outcomePtr(OutcomeTimeout)
		inv.CompletedAt = cloneTimePtr(&now)
		inv.QuotaReserved = false
		inv.LastError = "scheduled occurrence missed its start deadline"
		m.invocations[id] = inv
		m.syncInvocationOccurrenceLocked(inv, now)
	}
	return len(ids), nil
}

func (m *MemStore) syncInvocationOccurrenceLocked(inv Invocation, now time.Time) {
	if inv.OccurrenceID == "" {
		return
	}
	occurrence, ok := m.scheduleOccurrences[inv.OccurrenceID]
	if !ok {
		return
	}
	if inv.WorkDecision != nil {
		occurrence.WorkDecision = workpolicy.Clone(inv.WorkDecision)
	}
	occurrence.OutcomeCode = inv.OutcomeCode
	switch inv.State {
	case InvocationPending:
		if occurrence.StartedAt == nil {
			occurrence.Status = "queued"
		} else {
			occurrence.Status = "running"
		}
	case InvocationDispatching:
		occurrence.Status = "running"
		if occurrence.StartedAt == nil {
			occurrence.StartedAt = cloneTimePtr(&now)
		}
	case InvocationCompleted:
		occurrence.Status = "succeeded"
		if occurrence.FinishedAt == nil {
			occurrence.FinishedAt = cloneTimePtr(inv.CompletedAt)
		}
	case InvocationFailed, InvocationDeadLetter:
		if inv.State == InvocationFailed && inv.WorkDecision != nil && inv.WorkDecision.Classification == "uncertain" {
			occurrence.Status = "uncertain"
			occurrence.Reason = inv.WorkDecision.Reason
		} else if occurrence.StartedAt == nil && inv.StartDeadlineAt != nil && inv.StartDeadlineAt.Before(now) {
			occurrence.Status = "missed_deadline"
			occurrence.Reason = "invocation did not start before the occurrence start deadline"
		} else {
			occurrence.Status = "failed"
		}
		occurrence.FinishedAt = cloneTimePtr(inv.CompletedAt)
	case InvocationCancelled:
		occurrence.Status = "cancelled"
		occurrence.FinishedAt = cloneTimePtr(inv.CompletedAt)
	}
	occurrence.UpdatedAt = now
	m.scheduleOccurrences[occurrence.ID] = occurrence
}

var _ ScheduledCronInvocationStore = (*MemStore)(nil)
var _ ScheduledInvocationDeadlineStore = (*MemStore)(nil)
