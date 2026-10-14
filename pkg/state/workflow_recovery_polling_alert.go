package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pollingNotificationFromRaw(raw []byte) (*api.WorkflowRecoveryPollingNotification, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var n *api.WorkflowRecoveryPollingNotification
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("decode polling notification: %w", err)
	}
	return n, nil
}

func manualPollingNotificationFromRaw(raw []byte) (*api.WorkflowRecoveryPollingNotification, error) {
	var envelope struct {
		Notification *api.WorkflowRecoveryPollingNotification `json:"polling_notification"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode manual polling notification: %w", err)
	}
	return envelope.Notification, nil
}

func pollingAlertTransition(app App, scope, review string, version int64, code string, attempts int64, retry *time.Time, previous *api.WorkflowRecoveryPollingNotification, now time.Time) *api.WorkflowRecoveryPollingWebhookPayload {
	state := "failed"
	if code == "" {
		if previous == nil || previous.Payload.State != "failed" {
			return nil
		}
		state = "recovered"
		code = previous.Payload.Code
		attempts = previous.Payload.Attempts
	} else {
		if attempts < api.WorkflowRecoveryPollingAlertAttempts {
			return nil
		}
		if previous != nil && previous.Payload.State == "failed" && previous.Payload.Code == code && previous.Payload.PolicyVersion == version {
			return nil
		}
	}
	sequence := int64(1)
	if previous != nil {
		sequence = previous.Payload.Sequence + 1
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("recovery-polling:%s:%s:%s:%d", app.ID, scope, review, sequence))).String()
	path := "/v1/apps/" + url.PathEscape(app.Slug) + "/recovery-policy"
	if scope == "manual_initial" {
		path = "/v1/apps/" + url.PathEscape(app.Slug) + "/initial-recovery-reviews/" + url.PathEscape(review)
	}
	return &api.WorkflowRecoveryPollingWebhookPayload{EventID: id, AppID: app.ID, App: app.Slug, Scope: scope, ReviewID: review, PolicyVersion: version, Sequence: sequence, State: state, Code: code, Attempts: attempts, ObservedAt: now.UTC().Truncate(time.Microsecond), RetryAt: retry, InspectionURL: path, MonitoringOnly: true}
}

func pollingAlertHistory(n api.WorkflowRecoveryPollingNotification) api.WorkflowRecoveryHistoryEntry {
	p := n.Payload
	return api.WorkflowRecoveryHistoryEntry{PollingNotification: &n, OccurredAt: p.ObservedAt, Kind: "polling_" + p.State, Outcome: p.State, Reason: p.Code, PolicyVersion: p.PolicyVersion, ReviewID: p.ReviewID, NotificationEventID: p.EventID, ActorKind: "system", ActorID: "recovery_polling_observer"}
}

func pollingAlertReceipt(p api.WorkflowRecoveryPollingWebhookPayload, ids []string) api.WorkflowRecoveryPollingNotification {
	status := "queued"
	if len(ids) == 0 {
		status = "no_receivers"
	}
	return api.WorkflowRecoveryPollingNotification{Payload: p, Status: status, CapturedAt: p.ObservedAt, ReceiverWebhookIDs: ids}
}

func (m *MemStore) capturePollingAlertLocked(app App, payload *api.WorkflowRecoveryPollingWebhookPayload) (*api.WorkflowRecoveryPollingNotification, error) {
	if payload == nil {
		return nil, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode polling alert: %w", err)
	}
	ids := []string{}
	for _, h := range m.appWebhooks {
		if h.Scope == AppWebhookScopeApp && sameMemUUID(h.AccountID, app.AccountID) && sameMemUUID(h.AppID, app.ID) && h.Enabled && appWebhookMatches(h.EventFilter, AppWebhookEventRecoveryPollingStatus) {
			ids = append(ids, h.ID)
		}
	}
	sort.Strings(ids)
	n := pollingAlertReceipt(*payload, ids)
	if p := m.recoveryPolicies[canonicalMemUUID(app.ID)]; p != nil {
		p.appendHistoryEvent(pollingAlertHistory(n), payload.ObservedAt)
	}
	if len(ids) > 0 {
		if m.appWebhookEventOutbox == nil {
			m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
		}
		m.appWebhookEventOutbox[payload.EventID] = appWebhookOutboxEvent{ID: payload.EventID, AccountID: app.AccountID, AppID: app.ID, Event: AppWebhookEventRecoveryPollingStatus, SourceID: payload.EventID, Payload: raw, RecipientWebhookIDs: ids, CreatedAt: payload.ObservedAt}
	}
	return &n, nil
}

func capturePollingAlertTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app App, payload *api.WorkflowRecoveryPollingWebhookPayload) (*api.WorkflowRecoveryPollingNotification, error) {
	if payload == nil {
		return nil, nil
	}
	ids, err := q.WorkflowRecoveryReviewNotificationReceivers(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationReceiversParams{AccountID: mustPgUUID(app.AccountID), AppID: mustPgUUID(app.ID), Event: string(AppWebhookEventRecoveryPollingStatus)})
	if err != nil {
		return nil, fmt.Errorf("select polling alert receivers: %w", err)
	}
	receivers := []string{}
	for _, id := range ids {
		receivers = append(receivers, uuidString(id))
	}
	n := pollingAlertReceipt(*payload, receivers)
	if err = appendRecoveryHistoryEvent(ctx, q, tx, app.ID, pollingAlertHistory(n)); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		raw, e := json.Marshal(payload)
		if e != nil {
			return nil, fmt.Errorf("encode polling alert: %w", e)
		}
		if err = q.WorkflowRecoveryReviewNotificationEnqueue(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationEnqueueParams{EventID: mustPgUUID(payload.EventID), AccountID: mustPgUUID(app.AccountID), AppID: mustPgUUID(app.ID), Event: string(AppWebhookEventRecoveryPollingStatus), ReviewID: mustPgUUID(payload.EventID), Payload: raw, ReceiverIds: ids, NowAt: pgtypeFromTime(payload.ObservedAt)}); err != nil {
			return nil, fmt.Errorf("enqueue polling alert: %w", err)
		}
	}
	return &n, nil
}
