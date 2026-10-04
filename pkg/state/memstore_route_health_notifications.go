package state

import (
	"sort"

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
	var recipients []string
	for id, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == app.ID && hook.AccountID == app.AccountID && hook.Enabled && appWebhookMatches(hook.EventFilter, next.Event) {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return prepared, nil
	}
	sort.Strings(recipients)
	prepared.Outbox = &appWebhookOutboxEvent{ID: uuid.NewString(), AppID: app.ID, AccountID: app.AccountID, Event: next.Event,
		SourceID: entry.ID, Payload: next.Payload, RecipientWebhookIDs: recipients, CreatedAt: entry.CheckedAt}
	return prepared, nil
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
