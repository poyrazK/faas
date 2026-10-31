package state

// adr: 20261031130000 — a deploy's gregale.yaml is the source of truth for the
// app's event subscriptions: undeclared ones are disabled, and the enabled
// count is capped per app and per billing subject.

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EventSubscriptionFilterReasonDisabled settles a recipient whose
// subscription was disabled or removed after the event was accepted.
const EventSubscriptionFilterReasonDisabled = "subscription_disabled"

// EventSubscriptionReconcileStore lets a deploy turn subscriptions off and
// back on, and count them against the plan's caps.
type EventSubscriptionReconcileStore interface {
	// SetEventSubscriptionEnabled turns one subscription off or back on.
	SetEventSubscriptionEnabled(ctx context.Context, id, accountID, appID string, enabled bool) error
	// EventSubscriptionQuotaCounts returns the enabled subscriptions of the
	// app and of every app sharing its billing subject.
	EventSubscriptionQuotaCounts(ctx context.Context, appID string) (app, account int, err error)
}

// SetEventSubscriptionEnabled implements EventSubscriptionReconcileStore.
func (s *PgStore) SetEventSubscriptionEnabled(ctx context.Context, id, accountID, appID string, enabled bool) error {
	n, err := sqlc.New().SetEventSubscriptionEnabled(ctx, s.pool, sqlc.SetEventSubscriptionEnabledParams{
		Enabled: enabled, ID: mustPgUUID(id), AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID),
	})
	if err != nil {
		return fmt.Errorf("state: set event subscription %s enabled=%t: %w", id, enabled, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// EventSubscriptionQuotaCounts implements EventSubscriptionReconcileStore.
func (s *PgStore) EventSubscriptionQuotaCounts(ctx context.Context, appID string) (int, int, error) {
	row, err := sqlc.New().CountEnabledEventSubscriptionsForQuota(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return 0, 0, fmt.Errorf("state: count event subscriptions for app %s: %w", appID, err)
	}
	return int(row.AppCount), int(row.AccountCount), nil
}

// SetEventSubscriptionEnabled implements EventSubscriptionReconcileStore.
func (m *MemStore) SetEventSubscriptionEnabled(_ context.Context, id, accountID, appID string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, subscription := range m.eventSubscriptions {
		if subscription.ID != id {
			continue
		}
		if subscription.AccountID != canonicalMemUUID(accountID) || subscription.AppID != canonicalMemUUID(appID) {
			return ErrNotFound
		}
		subscription.Enabled = enabled
		subscription.UpdatedAt = time.Now().UTC()
		m.eventSubscriptions[key] = subscription
		return nil
	}
	return ErrNotFound
}

// EventSubscriptionQuotaCounts implements EventSubscriptionReconcileStore.
// The in-memory store has no organizations, so the account is the app's.
func (m *MemStore) EventSubscriptionQuotaCounts(_ context.Context, appID string) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.eventSubscriptionAppLocked(appID)
	if !ok {
		return 0, 0, ErrNotFound
	}
	var perApp, perAccount int
	for _, subscription := range m.eventSubscriptions {
		if !subscription.Enabled || subscription.AccountID != canonicalMemUUID(app.AccountID) {
			continue
		}
		if other, ok := m.eventSubscriptionAppLocked(subscription.AppID); !ok || other.Status == AppDeleted {
			continue
		}
		perAccount++
		if subscription.AppID == canonicalMemUUID(appID) {
			perApp++
		}
	}
	return perApp, perAccount, nil
}
