package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func realtimeScheduleCompletionEvent(row ManagedRealtimeSchedule, kind string) AppWebhookEvent {
	switch {
	case kind == "published":
		return AppWebhookEventRealtimeSchedulePublished
	case kind == "skipped":
		return AppWebhookEventRealtimeScheduleSkipped
	case kind == "attempt_failed" && row.Status == "failed":
		return AppWebhookEventRealtimeScheduleFailed
	}
	return ""
}
func realtimeScheduleCompletionPayload(row ManagedRealtimeSchedule, kind, eventID, appID string) api.RealtimeScheduleCompletionWebhookPayload {
	payload := api.RealtimeScheduleCompletionWebhookPayload{EventID: eventID, AppID: appID, EndpointID: row.EndpointID, Channel: row.Channel, ScheduleID: row.ID, Version: row.Version, Occurrence: row.Occurrence, CompletedOccurrences: row.CompletedOccurrences, SkippedOccurrences: row.SkippedOccurrences, Outcome: row.Status, Attempts: row.Attempts, CycleAttempts: row.CycleAttempts, DeliverAt: row.DeliverAt, OccurredAt: row.UpdatedAt}
	if kind == "published" {
		payload.Sequence = row.Sequence
	}
	if kind == "attempt_failed" {
		payload.FailureCode = row.LastError
	}
	if kind == "skipped" {
		payload.FailureCode = "realtime_schedule_condition_failed"
		payload.SkipReason = row.SkipReason
	}
	return payload
}

// Capture recipients and event identity alongside the outcome; the existing
// outbox relay and signed webhook dispatcher handle delivery and redelivery.
func (m *MemStore) enqueueRealtimeScheduleCompletionLocked(row ManagedRealtimeSchedule, kind string) {
	event := realtimeScheduleCompletionEvent(row, kind)
	if event == "" {
		return
	}
	ep, ok := m.managedRealtimeEndpoints[row.EndpointID]
	if !ok {
		return
	}
	recipients := make([]string, 0)
	for id, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == ep.AppID && hook.AccountID == ep.AccountID && hook.Enabled && appWebhookMatches(hook.EventFilter, event) {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return
	}
	sort.Strings(recipients)
	id := newID()
	payload, _ := json.Marshal(realtimeScheduleCompletionPayload(row, kind, id, ep.AppID))
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = make(map[string]appWebhookOutboxEvent)
	}
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: ep.AccountID, AppID: ep.AppID, Event: event, SourceID: id, Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: row.UpdatedAt}
}
func enqueueRealtimeScheduleCompletionPG(ctx context.Context, tx pgx.Tx, row ManagedRealtimeSchedule, kind string) error {
	event := realtimeScheduleCompletionEvent(row, kind)
	if event == "" {
		return nil
	}
	var appID, accountID string
	var recipients []string
	err := tx.QueryRow(ctx, `select e.app_id,e.account_id,array_agg(h.id::text order by h.id)
 from managed_realtime_endpoints e join app_webhooks h on h.app_id=e.app_id and h.account_id=e.account_id
 where e.id=$1 and h.scope='app' and h.enabled and (cardinality(h.event_filter)=0 or $2=any(h.event_filter)) group by e.app_id,e.account_id`, row.EndpointID, string(event)).Scan(&appID, &accountID, &recipients)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	id := newID()
	payload, err := json.Marshal(realtimeScheduleCompletionPayload(row, kind, id, appID))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into app_webhook_event_outbox(id,account_id,app_id,event,source_id,payload,recipient_webhook_ids) values($1,$2,$3,$4,$1,$5,$6::text[]::uuid[])`, id, accountID, appID, string(event), payload, recipients)
	return err
}
