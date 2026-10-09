package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Notification capture is a scheduler write; reporting never captures events.
type EventRecoveryExecutionNotificationStore interface {
	ProcessNextEventRecoveryExecutionNotification(context.Context, time.Time) (bool, error)
}

var _ EventRecoveryExecutionNotificationStore = (*PgStore)(nil)
var _ EventRecoveryExecutionNotificationStore = (*MemStore)(nil)

func recoveryExecutionNotificationPayload(job api.EventRecoveryJob, summary api.EventRecoveryExecutionSummary, now time.Time) (api.EventRecoveryExecutionFinishedWebhookPayload, error) {
	_, base, err := recoveryNotificationPayload(job, "completed")
	if err != nil {
		return api.EventRecoveryExecutionFinishedWebhookPayload{}, err
	}
	if job.Selection.Mode != "execution" || job.PendingCount != 0 || job.QueuedCount == 0 || summary.TrackedCount != job.QueuedCount || summary.SavedResults != job.QueuedCount || summary.Queued+summary.Running+summary.Retrying+summary.Unknown != 0 {
		return api.EventRecoveryExecutionFinishedWebhookPayload{}, fmt.Errorf("recovery execution is unresolved")
	}
	base.EventID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:event-recovery:"+job.ID+":execution_finished")).String()
	base.Outcome = "finished_with_non_success"
	if summary.Succeeded == job.QueuedCount {
		base.Outcome = "all_succeeded"
	}
	return api.EventRecoveryExecutionFinishedWebhookPayload{EventRecoveryFinishedWebhookPayload: base, Execution: summary, ExecutionFinishedAt: now}, nil
}

func (s *PgStore) ProcessNextEventRecoveryExecutionNotification(ctx context.Context, now time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	candidate, err := q.EventRecoveryClaimExecutionNotification(ctx, tx, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim recovery execution notification: %w", err)
	}
	job, err := getEventRecoveryMetadata(ctx, q, tx, uuidString(candidate.AccountID), uuidString(candidate.ID))
	if err != nil {
		return false, err
	}
	if job.QueuedCount == 0 {
		if err = q.EventRecoveryCaptureExecutionNotification(ctx, tx, sqlc.EventRecoveryCaptureExecutionNotificationParams{JobID: candidate.ID}); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	observations, err := observeRecoveryExecutions(ctx, q, tx, uuidString(candidate.AccountID), job.ID, 0, api.EventRecoveryRecipientsMax, now)
	if err != nil {
		return false, err
	}
	summary := api.EventRecoveryExecutionSummary{ObservedAt: now}
	for _, execution := range observations {
		addRecoveryExecution(&summary, execution)
	}
	payload, err := recoveryExecutionNotificationPayload(job, summary, now)
	if err != nil {
		if err = q.EventRecoveryDeferExecutionNotification(ctx, tx, sqlc.EventRecoveryDeferExecutionNotificationParams{JobID: candidate.ID, NextAt: pgtypeFromTime(now.Add(api.EventRecoveryExecutionNotificationPollInterval))}); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	if err = q.EventRecoveryCaptureExecutionNotification(ctx, tx, sqlc.EventRecoveryCaptureExecutionNotificationParams{JobID: candidate.ID, FinishedAt: pgtypeFromTime(now)}); err != nil {
		return false, err
	}
	if err = q.EventRecoveryEnqueueNotification(ctx, tx, sqlc.EventRecoveryEnqueueNotificationParams{JobID: candidate.ID, EventID: mustPgUUID(payload.EventID), Event: string(AppWebhookEventRecoveryExecutionFinished), Payload: data}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (m *MemStore) ProcessNextEventRecoveryExecutionNotification(ctx context.Context, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	jobs := make([]*memEventRecoveryJob, 0, len(m.eventRecoveryJobs))
	for _, job := range m.eventRecoveryJobs {
		if job.Job.Selection.Mode == "execution" && !job.ExecutionNotificationCaptured && !job.NextExecutionNotificationAt.After(now) && (job.Job.State == "completed" || job.Job.State == "cancelled") && job.Job.CompletedAt != nil && !job.Job.CompletedAt.After(now) {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].NextExecutionNotificationAt.Equal(jobs[j].NextExecutionNotificationAt) {
			return jobs[i].Job.ID < jobs[j].Job.ID
		}
		return jobs[i].NextExecutionNotificationAt.Before(jobs[j].NextExecutionNotificationAt)
	})
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		response := m.eventRecoveryObservedResponseLocked(job, now)
		if response.QueuedCount == 0 {
			job.ExecutionNotificationCaptured = true
			return true, nil
		}
		payload, err := recoveryExecutionNotificationPayload(response, *response.Execution, now)
		if err != nil {
			job.NextExecutionNotificationAt = now.Add(api.EventRecoveryExecutionNotificationPollInterval)
			return true, nil
		}
		job.ExecutionNotificationCaptured = true
		job.Job.ExecutionFinishedAt = cloneEventReceiptTime(&now)
		m.enqueueRecoveryExecutionNotificationLocked(job, payload)
		return true, nil
	}
	return false, nil
}

func (m *MemStore) enqueueRecoveryExecutionNotificationLocked(job *memEventRecoveryJob, payload api.EventRecoveryExecutionFinishedWebhookPayload) {
	if _, captured := job.NotificationReceipts[string(AppWebhookEventRecoveryExecutionFinished)]; captured {
		return
	}
	recipients := []string{}
	for _, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && sameMemUUID(hook.AccountID, job.AccountID) && sameMemUUID(hook.AppID, job.Job.AppID) && hook.Enabled && appWebhookMatches(hook.EventFilter, AppWebhookEventRecoveryExecutionFinished) {
			recipients = append(recipients, hook.ID)
		}
	}
	sort.Strings(recipients)
	if job.NotificationReceipts == nil {
		job.NotificationReceipts = map[string]recoveryNotificationReceipt{}
	}
	job.NotificationReceipts[string(AppWebhookEventRecoveryExecutionFinished)] = recoveryNotificationReceipt{EventID: payload.EventID, CapturedAt: payload.ExecutionFinishedAt, RecipientWebhookIDs: append([]string{}, recipients...)}
	if len(recipients) == 0 {
		return
	}
	data, _ := json.Marshal(payload)
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[payload.EventID] = appWebhookOutboxEvent{ID: payload.EventID, AccountID: job.AccountID, AppID: job.Job.AppID, Event: AppWebhookEventRecoveryExecutionFinished, SourceID: job.Job.ID, Payload: data, RecipientWebhookIDs: recipients, CreatedAt: payload.ExecutionFinishedAt}
}
