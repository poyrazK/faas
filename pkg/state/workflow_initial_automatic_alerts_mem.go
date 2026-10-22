package state

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

func (m *MemStore) captureInitialAutomaticBlockLocked(account string, r *api.WorkflowInitialRecoveryReview, reason string, now time.Time) error {
	if !initialAttentionNeedsDiagnosis(*r, reason) {
		return nil
	}
	d := m.initialEvidenceLocked(*r, now)
	payload := initialAutomaticBlockTransition(r, reason, now, d)
	if payload == nil {
		return nil
	}
	receipt, err := m.captureInitialRecoveryAttentionLocked(account, r, payload, r.AutomaticBlockSequence, now)
	if err != nil {
		return err
	}
	r.AutomaticBlockNotification = receipt
	return nil
}

func (m *MemStore) captureInitialRecoveryAttentionLocked(account string, r *api.WorkflowInitialRecoveryReview, payload *api.WorkflowRecoveryQueueBlockedWebhookPayload, sequence int64, now time.Time) (*api.WorkflowRecoveryAutomaticBlockedNotification, error) {
	ids := []string{}
	for _, h := range m.appWebhooks {
		if h.Scope == AppWebhookScopeApp && sameMemUUID(h.AccountID, account) && sameMemUUID(h.AppID, r.AppID) && h.Enabled && appWebhookMatches(h.EventFilter, AppWebhookEventRecoveryQueueBlocked) {
			ids = append(ids, h.ID)
		}
	}
	sort.Strings(ids)
	receipt := automaticBlockedReceipt(*payload, ids)
	receipt.Sequence = sequence
	if p := m.recoveryPolicies[canonicalMemUUID(r.AppID)]; p != nil {
		p.appendHistoryEvent(initialAlertHistory(*r, *payload), now)
	}
	if len(ids) == 0 {
		return &receipt, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode initial recovery attention: %w", err)
	}
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[payload.EventID] = appWebhookOutboxEvent{ID: payload.EventID, AccountID: account, AppID: r.AppID, Event: AppWebhookEventRecoveryQueueBlocked, SourceID: payload.EventID, Payload: raw, RecipientWebhookIDs: ids, CreatedAt: now}
	return &receipt, nil
}
