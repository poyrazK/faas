package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prepare before touching traffic; publish only with the held or successful
// advance. This mirrors transactional recipient snapshots without fallible
// serialization after a MemStore traffic mutation.
type memRouteHealthNotification struct {
	State  routeHealthNotificationState
	Outbox *appWebhookOutboxEvent
}

func (m *MemStore) prepareRouteHealthNotificationLocked(entry api.RouteHealthHistoryEntry) (memRouteHealthNotification, error) {
	if entry.ID == "" {
		return memRouteHealthNotification{}, nil
	}
	app := m.apps[entry.Report.AppID]
	if app.Status == AppDeleted || !m.accounts[app.AccountID].MayDeploy() {
		return memRouteHealthNotification{}, nil
	}
	previous := m.routeHealthNotifications[entry.Report.DeploymentID]
	next, err := routeHealthTransition(previous, entry, app.Slug)
	if err != nil {
		return memRouteHealthNotification{}, err
	}
	prepared := memRouteHealthNotification{State: next.State}
	if next.Event == "" {
		return prepared, nil
	}
	prepared.Outbox = m.routeHealthOutboxLocked(app, next.Event, entry.ID, next.Payload, entry.CheckedAt)
	return prepared, nil
}

// routeHealthOutboxLocked snapshots the app webhooks subscribed to event; nil
// when none is.
func (m *MemStore) routeHealthOutboxLocked(app App, event AppWebhookEvent, sourceID string, payload []byte, at time.Time) *appWebhookOutboxEvent {
	var recipients []string
	for id, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == app.ID && hook.AccountID == app.AccountID && hook.Enabled && appWebhookMatches(hook.EventFilter, event) {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	sort.Strings(recipients)
	return &appWebhookOutboxEvent{ID: uuid.NewString(), AppID: app.ID, AccountID: app.AccountID, Event: event,
		SourceID: sourceID, Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: at}
}

func (m *MemStore) publishRouteHealthNotificationLocked(deploymentID string, prepared memRouteHealthNotification) {
	if prepared.State.ContextKey == "" {
		return
	}
	if m.routeHealthNotifications == nil {
		m.routeHealthNotifications = map[string]routeHealthNotificationState{}
	}
	m.routeHealthNotifications[deploymentID] = prepared.State
	if prepared.Outbox == nil {
		return
	}
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[prepared.Outbox.ID] = *prepared.Outbox
}

// AbortRouteHealthHold mirrors the Postgres record of a hold the hold
// deadline ended (ADR-20261028110044 amendment 2).
func (m *MemStore) AbortRouteHealthHold(_ context.Context, accountID, appID, deploymentID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return ErrNotFound
	}
	held, ok := m.routeHealthNotifications[deploymentID]
	if !ok || held.Status != "blocked_unknown" && held.Status != "blocked_regressed" {
		return nil
	}
	m.routeHealthNotifications[deploymentID] = routeHealthNotificationState{ContextKey: held.ContextKey, Status: "aborted", Since: at.UTC()}
	if app.Status == AppDeleted || !m.accounts[accountID].MayDeploy() {
		return nil
	}
	for _, stored := range m.routeHealthHistory[deploymentID] {
		if stored.ID != held.BlockedDecisionID {
			continue
		}
		entry, err := decodeRouteHealthHistory(stored.Body, appID, deploymentID)
		if err != nil {
			return err
		}
		payload, err := routeHealthHoldAbortedPayload(entry, app.Slug, at)
		if err != nil {
			return err
		}
		if outbox := m.routeHealthOutboxLocked(app, AppWebhookEventRouteHealthAborted, entry.ID, payload, at); outbox != nil {
			if m.appWebhookEventOutbox == nil {
				m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
			}
			m.appWebhookEventOutbox[outbox.ID] = *outbox
		}
		return nil
	}
	return nil
}
