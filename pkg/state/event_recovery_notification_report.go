package state

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type EventRecoveryNotificationsStore interface {
	GetEventRecoveryNotifications(context.Context, string, string, time.Time) (api.EventRecoveryNotifications, error)
}

// Frozen at capture, including an empty receiver selection. No payload or secrets.
type recoveryNotificationReceipt struct {
	EventID             string    `json:"event_id"`
	CapturedAt          time.Time `json:"captured_at"`
	RecipientWebhookIDs []string  `json:"recipient_webhook_ids"`
}
type recoveryNotificationEvidence struct {
	Receipts   map[string]recoveryNotificationReceipt
	Outbox     map[string]recoveryNotificationReceipt
	Deliveries map[string][]api.EventRecoveryNotificationReceiver
	Available  map[string]bool
	Truncated  map[string]bool
}

var recoveryNotificationEvents = []string{"event_recovery.completed", "event_recovery.cancelled", "event_recovery.expired", "event_recovery.execution_finished"}

func recoveryNotificationEventID(job, event string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:event-recovery:"+job+":"+event[len("event_recovery."):])).String()
}
func validateRecoveryNotificationReceipts(job string, receipts map[string]recoveryNotificationReceipt) error {
	for event, receipt := range receipts {
		known := false
		for _, candidate := range recoveryNotificationEvents {
			if candidate == event {
				known = true
				break
			}
		}
		if !known || receipt.EventID != recoveryNotificationEventID(job, event) || receipt.CapturedAt.IsZero() || receipt.RecipientWebhookIDs == nil {
			return fmt.Errorf("invalid recovery notification receipt")
		}
		seen := map[string]bool{}
		for _, id := range receipt.RecipientWebhookIDs {
			parsed, err := uuid.Parse(id)
			if err != nil || parsed.String() != id || seen[id] {
				return fmt.Errorf("invalid recovery notification receiver identity")
			}
			seen[id] = true
		}
	}
	return nil
}

func recoveryNotificationReport(job api.EventRecoveryJob, slug string, executionCaptured bool, now time.Time, evidence recoveryNotificationEvidence) (api.EventRecoveryNotifications, error) {
	if err := validateRecoveryNotificationReceipts(job.ID, evidence.Receipts); err != nil {
		return api.EventRecoveryNotifications{}, err
	}
	out := api.EventRecoveryNotifications{JobID: job.ID, AppID: job.AppID, AppSlug: slug, ObservedAt: now, ReceiverLimit: api.EventRecoveryNotificationReceiversMax, Notifications: []api.EventRecoveryNotification{}}
	admission := api.EventRecoveryNotification{Kind: "admission", CaptureStatus: "pending", EvidenceSource: "unavailable", AcknowledgementStatus: "unacknowledged", Receivers: []api.EventRecoveryNotificationReceiver{}}
	if job.State == "completed" || job.State == "cancelled" {
		admission.CaptureStatus = "unknown"
		admission.AcknowledgementStatus = "unknown"
		if job.State == "completed" {
			admission.Event = "event_recovery.completed"
		}
		found := ""
		for _, event := range recoveryNotificationEvents[:3] {
			_, receipt := evidence.Receipts[event]
			_, outbox := evidence.Outbox[event]
			if receipt || outbox || len(evidence.Deliveries[event]) > 0 {
				if found != "" {
					return out, fmt.Errorf("ambiguous recovery admission notification evidence")
				}
				found = event
			}
		}
		if found != "" {
			admission.Event = found
		}
		if admission.Event != "" {
			admission = observeRecoveryNotification(job.ID, slug, admission, evidence)
		}
	}
	out.Notifications = append(out.Notifications, admission)
	execution := api.EventRecoveryNotification{Kind: "execution", Event: "event_recovery.execution_finished", CaptureStatus: "pending", EvidenceSource: "unavailable", AcknowledgementStatus: "unacknowledged", Receivers: []api.EventRecoveryNotificationReceiver{}}
	if job.Selection.Mode != "execution" || (job.State == "completed" || job.State == "cancelled") && job.QueuedCount == 0 || executionCaptured && job.ExecutionFinishedAt == nil {
		execution.CaptureStatus = "not_applicable"
		execution.AcknowledgementStatus = "not_applicable"
	} else {
		if job.ExecutionFinishedAt != nil {
			execution.CaptureStatus = "captured"
			execution.CapturedAt = cloneEventReceiptTime(job.ExecutionFinishedAt)
			execution.AcknowledgementStatus = "unknown"
		}
		execution = observeRecoveryNotification(job.ID, slug, execution, evidence)
	}
	out.Notifications = append(out.Notifications, execution)
	return out, nil
}

func observeRecoveryNotification(job, slug string, out api.EventRecoveryNotification, evidence recoveryNotificationEvidence) api.EventRecoveryNotification {
	event := out.Event
	out.EventID = recoveryNotificationEventID(job, event)
	receipt, known := evidence.Receipts[event]
	if known {
		out.EvidenceSource = "capture_snapshot"
	} else if retained, ok := evidence.Outbox[event]; ok {
		receipt = retained
		known = true
		out.EvidenceSource = "retained_outbox"
	}
	rows := evidence.Deliveries[event]
	if known {
		out.CaptureStatus = "captured"
		out.CapturedAt = cloneEventReceiptTime(&receipt.CapturedAt)
		out.RecipientsKnown = true
		count := int64(len(receipt.RecipientWebhookIDs))
		out.SelectedRecipientCount = &count
		ids := append([]string{}, receipt.RecipientWebhookIDs...)
		sort.Strings(ids)
		out.CountsComplete = len(ids) <= api.EventRecoveryNotificationReceiversMax
		ids = ids[:min(len(ids), api.EventRecoveryNotificationReceiversMax)]
		indexed := map[string]api.EventRecoveryNotificationReceiver{}
		for _, row := range rows {
			indexed[row.WebhookID] = row
		}
		_, awaitingRelay := evidence.Outbox[event]
		for _, id := range ids {
			row, ok := indexed[id]
			if !ok {
				row = api.EventRecoveryNotificationReceiver{WebhookID: id, ReceiverAvailable: evidence.Available[id], Status: "unknown"}
				if awaitingRelay {
					row.Status = "awaiting_relay"
				}
			}
			out.Receivers = append(out.Receivers, row)
		}
	} else if len(rows) > 0 {
		out.CaptureStatus = "captured"
		out.EvidenceSource = "retained_deliveries"
		out.Receivers = append(out.Receivers, rows[:min(len(rows), api.EventRecoveryNotificationReceiversMax)]...)
	}
	out.CountsComplete = out.CountsComplete && !evidence.Truncated[event]
	for i := range out.Receivers {
		row := &out.Receivers[i]
		if row.Status != "pending" && row.Status != "failed" {
			row.NextAttemptAt = nil
		}
		if row.DeliveryID != "" && row.ReceiverAvailable {
			base := "/v1/apps/" + url.PathEscape(slug) + "/webhooks/" + url.PathEscape(row.WebhookID) + "/deliveries/" + url.PathEscape(row.DeliveryID)
			row.AttemptsPath = base + "/attempts"
			if row.Status == "dead" {
				row.RetryPath = base + "/retry"
			}
		}
		switch row.Status {
		case "pending":
			out.PendingCount++
		case "in_flight":
			out.InFlightCount++
		case "succeeded":
			out.SucceededCount++
		case "failed":
			out.FailedCount++
		case "dead":
			out.DeadCount++
		case "awaiting_relay":
			out.AwaitingRelayCount++
		default:
			out.UnknownCount++
		}
	}
	switch {
	case !out.CountsComplete || out.UnknownCount > 0:
		out.AcknowledgementStatus = "unknown"
	case len(out.Receivers) == 0:
		out.AcknowledgementStatus = "no_receivers"
	case out.SucceededCount == int64(len(out.Receivers)):
		out.AcknowledgementStatus = "acknowledged"
	default:
		out.AcknowledgementStatus = "unacknowledged"
	}
	// An uncaptured event has no receiver selection yet, rather than missing history.
	if out.CaptureStatus == "pending" {
		out.AcknowledgementStatus = "unacknowledged"
	}
	return out
}

func (m *MemStore) GetEventRecoveryNotifications(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryNotifications, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotifications
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return out, err
	}
	return m.recoveryNotificationReportLocked(ctx, entry, now)
}

func (m *MemStore) recoveryNotificationReportLocked(ctx context.Context, entry *memEventRecoveryJob, now time.Time) (api.EventRecoveryNotifications, error) {
	var out api.EventRecoveryNotifications
	account, id := entry.AccountID, entry.Job.ID
	// Copy metadata and counts without the legacy running-to-completed response helper.
	job := entry.Job
	job.QueuedCount = 0
	for _, item := range entry.Items {
		if item.State == "queued" {
			job.QueuedCount++
		}
	}
	app, ok := m.eventSubscriptionAppLocked(job.AppID)
	if !ok {
		return out, ErrNotFound
	}
	evidence := recoveryNotificationEvidence{Receipts: entry.NotificationReceipts, Outbox: map[string]recoveryNotificationReceipt{}, Deliveries: map[string][]api.EventRecoveryNotificationReceiver{}, Available: map[string]bool{}, Truncated: map[string]bool{}}
	for _, event := range m.appWebhookEventOutbox {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if sameMemUUID(event.AccountID, account) && sameMemUUID(event.AppID, job.AppID) && sameMemUUID(event.SourceID, id) {
			for _, name := range recoveryNotificationEvents {
				if string(event.Event) == name && event.ID == recoveryNotificationEventID(job.ID, name) {
					evidence.Outbox[name] = recoveryNotificationReceipt{event.ID, event.CreatedAt, event.RecipientWebhookIDs}
				}
			}
		}
	}
	for _, hook := range m.appWebhooks {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if hook.Scope == AppWebhookScopeApp && sameMemUUID(hook.AccountID, account) && sameMemUUID(hook.AppID, job.AppID) {
			evidence.Available[hook.ID] = true
		}
	}
	for _, event := range recoveryNotificationEvents {
		for webhookID, deliveryID := range entry.NotificationDeliveryIDs[event] {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			delivery, ok := m.appWebhookDeliveries[deliveryID]
			if !ok || !sameMemUUID(delivery.AccountID, account) || !sameMemUUID(delivery.AppID, job.AppID) || !sameMemUUID(delivery.WebhookID, webhookID) || string(delivery.Event) != event {
				continue
			}
			row := api.EventRecoveryNotificationReceiver{WebhookID: delivery.WebhookID, ReceiverAvailable: evidence.Available[delivery.WebhookID], DeliveryID: delivery.ID, Status: string(delivery.Status), Attempt: delivery.Attempt, ReplayGeneration: m.appWebhookReplayGenerations[delivery.ID], LastResponseCode: delivery.LastResponseCode, NextAttemptAt: cloneEventReceiptTime(&delivery.NextAttemptAt), DeliveredAt: cloneEventReceiptTime(delivery.DeliveredAt)}
			rows := append(evidence.Deliveries[event], row)
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].WebhookID == rows[j].WebhookID {
					return rows[i].DeliveryID < rows[j].DeliveryID
				}
				return rows[i].WebhookID < rows[j].WebhookID
			})
			if len(rows) > api.EventRecoveryNotificationReceiversMax+1 {
				rows = rows[:api.EventRecoveryNotificationReceiversMax+1]
			}
			evidence.Deliveries[event] = rows
		}
	}
	for event, rows := range evidence.Deliveries {
		evidence.Truncated[event] = len(rows) > api.EventRecoveryNotificationReceiversMax
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return recoveryNotificationReport(job, app.Slug, entry.ExecutionNotificationCaptured, now, evidence)
}

// Preserve the relay's exact source-event/receiver association without inspecting
// payloads or changing the general webhook response shape. Job pruning drops it.
func (m *MemStore) trackRecoveryNotificationDeliveryLocked(event appWebhookOutboxEvent, webhookID, deliveryID string) {
	job := m.eventRecoveryJobs[event.SourceID]
	if job == nil || !sameMemUUID(job.AccountID, event.AccountID) || !sameMemUUID(job.Job.AppID, event.AppID) {
		return
	}
	for _, name := range recoveryNotificationEvents {
		if string(event.Event) != name || event.ID != recoveryNotificationEventID(job.Job.ID, name) {
			continue
		}
		if job.NotificationDeliveryIDs == nil {
			job.NotificationDeliveryIDs = map[string]map[string]string{}
		}
		if job.NotificationDeliveryIDs[name] == nil {
			job.NotificationDeliveryIDs[name] = map[string]string{}
		}
		job.NotificationDeliveryIDs[name][webhookID] = deliveryID
		return
	}
}
