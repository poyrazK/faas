package state

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) notificationRetryHooksLocked(account, app string) map[string]bool {
	out := map[string]bool{}
	for _, h := range m.appWebhooks {
		if sameMemUUID(h.AccountID, account) && sameMemUUID(h.AppID, app) && h.Scope == AppWebhookScopeApp {
			out[h.ID] = h.Enabled
		}
	}
	return out
}
func (m *MemStore) PreviewEventRecoveryNotificationRetry(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryNotificationRetryPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryPreview
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return out, err
	}
	report, err := m.recoveryNotificationReportLocked(ctx, entry, now)
	if err != nil {
		return out, err
	}
	return recoveryNotificationRetryPreview(report, m.notificationRetryHooksLocked(account, report.AppID), recoveryNotificationRetryAllowed(m.accounts[account].Plan)), ctx.Err()
}
func (m *MemStore) RetryEventRecoveryNotifications(ctx context.Context, account, id string, req api.EventRecoveryNotificationRetryRequest, now time.Time) (api.EventRecoveryNotificationRetryResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryResponse
	if err := validateRecoveryNotificationRetry(account, id, now, &req); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	entry, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return out, err
	}
	receipts := map[string]json.RawMessage{}
	for key, value := range entry.NotificationRetryReceipts {
		receipts[key] = value
	}
	if prior, ok, err := recoveryNotificationRetryPrior(receipts, req); ok || err != nil {
		return prior, err
	}
	if len(receipts) >= api.EventRecoveryNotificationRetryReceiptsMax {
		return out, ErrEventRecoveryNotificationRetryLimit
	}
	report, err := m.recoveryNotificationReportLocked(ctx, entry, now)
	if err != nil {
		return out, err
	}
	enabled := m.notificationRetryHooksLocked(account, report.AppID)
	allowed := recoveryNotificationRetryAllowed(m.accounts[account].Plan)
	out = api.EventRecoveryNotificationRetryResponse{JobID: report.JobID, AppID: report.AppID, RequestID: req.RequestID, DecidedAt: now, Results: []api.EventRecoveryNotificationRetryResult{}}
	changes := map[string]AppWebhookDelivery{}
	generations := map[string]int{}
	for _, target := range req.Targets {
		receiver, _, reason := recoveryNotificationRetryReceiver(report, target)
		on, exists := enabled[target.WebhookID]
		if reason == "" {
			reason = recoveryNotificationRetryReason(receiver.DeliveryID, receiver.Status, receiver.ReplayGeneration, exists, on, allowed, target.ExpectedReplayGeneration)
		}
		result := api.EventRecoveryNotificationRetryResult{Target: target, State: "skipped", Reason: reason}
		if reason == "" {
			d := m.appWebhookDeliveries[target.DeliveryID]
			d.Status = AppWebhookDeliveryPending
			d.Attempt = 0
			d.LastError = ""
			d.LastResponseCode = 0
			d.NextAttemptAt = now
			d.UpdatedAt = now
			changes[d.ID] = d
			n := receiver.ReplayGeneration + 1
			generations[d.ID] = n
			result.State = "queued"
			result.ReplayGeneration = &n
		}
		out.Results = append(out.Results, result)
	}
	actor := recoveryAuditActor(ctx, "notification_retry")
	raw, err := json.Marshal(recoveryNotificationRetryReceipt{Request: req, Response: out, ActorKind: actor.Kind, ActorID: actor.ID})
	if err != nil {
		return api.EventRecoveryNotificationRetryResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryNotificationRetryResponse{}, err
	}
	for id, d := range changes {
		m.appWebhookDeliveries[id] = d
	}
	if m.appWebhookReplayGenerations == nil {
		m.appWebhookReplayGenerations = map[string]int{}
	}
	for id, n := range generations {
		m.appWebhookReplayGenerations[id] = n
	}
	if entry.NotificationRetryReceipts == nil {
		entry.NotificationRetryReceipts = map[string][]byte{}
	}
	entry.NotificationRetryReceipts[req.RequestID] = raw
	return out, nil
}

func (m *MemStore) GetEventRecoveryNotificationRetryHistory(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryNotificationRetryHistory, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryHistory
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	entry, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return out, err
	}
	receipts := map[string]json.RawMessage{}
	for key, raw := range entry.NotificationRetryReceipts {
		receipts[key] = json.RawMessage(raw)
	}
	return recoveryNotificationRetryHistory(entry.Job.ID, entry.Job.AppID, now, receipts)
}
func (m *MemStore) GetEventRecoveryNotificationRetryDecision(ctx context.Context, account, id, requestID string, now time.Time) (api.EventRecoveryNotificationRetryDecisionDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	var out api.EventRecoveryNotificationRetryDecisionDetail
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if now.IsZero() {
		return out, ErrEventRecoveryQuery
	}
	parsed, err := uuid.Parse(requestID)
	if err != nil || parsed == uuid.Nil || parsed.String() != requestID {
		return out, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	entry, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return out, err
	}
	raw, ok := entry.NotificationRetryReceipts[requestID]
	if !ok {
		return out, ErrEventRecoveryNotificationRetryDecisionNotFound
	}
	var saved recoveryNotificationRetryReceipt
	if err := json.Unmarshal(raw, &saved); err != nil {
		return out, err
	}
	report, err := m.recoveryNotificationReportLocked(ctx, entry, now)
	if err != nil {
		return out, err
	}
	return recoveryNotificationRetryDecisionDetail(entry.Job.ID, entry.Job.AppID, requestID, now, saved, report)
}
