package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func recoveryNotificationPayload(job api.EventRecoveryJob, outcome string) (AppWebhookEvent, api.EventRecoveryFinishedWebhookPayload, error) {
	event := AppWebhookEvent("event_recovery." + outcome)
	if outcome != "completed" && outcome != "cancelled" && outcome != "expired" || job.CompletedAt == nil {
		return event, api.EventRecoveryFinishedWebhookPayload{}, fmt.Errorf("invalid recovery terminal notification")
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:event-recovery:"+job.ID+":"+outcome)).String()
	mode := job.Selection.Mode
	if mode == "" {
		mode = "routing"
	}
	out := api.EventRecoveryFinishedWebhookPayload{EventID: id, JobID: job.ID, AppID: job.AppID, Mode: mode, State: job.State, Outcome: outcome, SelectedCount: job.SelectedCount, PendingCount: job.PendingCount, QueuedCount: job.QueuedCount, SkippedCount: job.SkippedCount, CancelledCount: job.CancelledCount, CreatedAt: job.CreatedAt, ExpiresAt: job.ExpiresAt, CompletedAt: *job.CompletedAt}
	return event, out, nil
}
func enqueueRecoveryNotification(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, id, outcome string) error {
	account, err := q.EventRecoveryNotificationJob(ctx, tx, mustPgUUID(id))
	if err != nil {
		return err
	}
	job, err := getEventRecoveryMetadata(ctx, q, tx, uuidString(account), id)
	if err != nil {
		return err
	}
	event, payload, err := recoveryNotificationPayload(job, outcome)
	if err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return q.EventRecoveryEnqueueNotification(ctx, tx, sqlc.EventRecoveryEnqueueNotificationParams{JobID: mustPgUUID(id), EventID: mustPgUUID(payload.EventID), Event: string(event), Payload: data})
}
func (m *MemStore) enqueueRecoveryNotificationLocked(job *memEventRecoveryJob, outcome string) {
	if job.NotificationCaptured {
		return
	}
	response := memEventRecoveryResponse(job)
	event, payload, err := recoveryNotificationPayload(response, outcome)
	if err != nil {
		return
	}
	job.NotificationCaptured = true
	recipients := []string{}
	for _, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && sameMemUUID(hook.AccountID, job.AccountID) && sameMemUUID(hook.AppID, job.Job.AppID) && hook.Enabled && appWebhookMatches(hook.EventFilter, event) {
			recipients = append(recipients, hook.ID)
		}
	}
	if len(recipients) == 0 {
		return
	}
	sort.Strings(recipients)
	data, _ := json.Marshal(payload)
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[payload.EventID] = appWebhookOutboxEvent{ID: payload.EventID, AccountID: job.AccountID, AppID: job.Job.AppID, Event: event, SourceID: job.Job.ID, Payload: data, RecipientWebhookIDs: recipients, CreatedAt: *job.Job.CompletedAt}
}

func scheduleRecoveryNotification(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, params sqlc.EventRecoveryScheduleParams) error {
	state, err := q.EventRecoveryScheduleTerminalState(ctx, tx, sqlc.EventRecoveryScheduleTerminalStateParams(params))
	if err != nil {
		return err
	}
	if state == "completed" {
		return enqueueRecoveryNotification(ctx, q, tx, uuidString(params.JobID), "completed")
	}
	return nil
}
