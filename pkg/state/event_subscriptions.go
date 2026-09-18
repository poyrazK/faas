package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EventSubscription is the durable, tenant-scoped representation of an
// internal event subscription declared by an application manifest.
type EventSubscription struct {
	ID        string
	AccountID string
	AppID     string
	Source    string
	Type      string
	Filter    json.RawMessage
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// EventSubscriptionStore is optional on Store implementations so existing
// test doubles and external integrations do not need to grow immediately.
type EventSubscriptionStore interface {
	ListEventSubscriptionsForApp(context.Context, string) ([]EventSubscription, error)
	ListEnabledEventSubscriptionsForAccount(context.Context, string) ([]EventSubscription, error)
	UpsertEventSubscription(context.Context, string, string, string, string, json.RawMessage) (EventSubscription, bool, error)
	DeleteEventSubscription(context.Context, string, string, string) error
}

func normalizeEventSubscriptionFilter(filter json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(filter)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}"), nil
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("event subscription filter must be a JSON object")
	}
	return json.Marshal(value)
}

func eventSubscriptionFromSQL(row sqlc.EventSubscription) EventSubscription {
	return EventSubscription{
		ID:        uuidFromPgtype(row.ID).String(),
		AccountID: uuidFromPgtype(row.AccountID).String(),
		AppID:     uuidFromPgtype(row.AppID).String(),
		Source:    row.Source,
		Type:      row.Type,
		Filter:    append(json.RawMessage(nil), row.Filter...),
		Enabled:   row.Enabled,
		CreatedAt: timeFromPgtype(row.CreatedAt),
		UpdatedAt: timeFromPgtype(row.UpdatedAt),
	}
}

func eventSubscriptionFromUpsert(row sqlc.UpsertEventSubscriptionRow) EventSubscription {
	return EventSubscription{
		ID:        uuidFromPgtype(row.ID).String(),
		AccountID: uuidFromPgtype(row.AccountID).String(),
		AppID:     uuidFromPgtype(row.AppID).String(),
		Source:    row.Source,
		Type:      row.Type,
		Filter:    append(json.RawMessage(nil), row.Filter...),
		Enabled:   row.Enabled,
		CreatedAt: timeFromPgtype(row.CreatedAt),
		UpdatedAt: timeFromPgtype(row.UpdatedAt),
	}
}

// ListEventSubscriptionsForApp returns subscriptions in stable creation order.
func (s *PgStore) ListEventSubscriptionsForApp(ctx context.Context, appID string) ([]EventSubscription, error) {
	rows, err := sqlc.New().ListEventSubscriptionsForApp(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, err
	}
	out := make([]EventSubscription, len(rows))
	for i, row := range rows {
		out[i] = eventSubscriptionFromSQL(row)
	}
	return out, nil
}

func (s *PgStore) ListEnabledEventSubscriptionsForAccount(ctx context.Context, accountID string) ([]EventSubscription, error) {
	rows, err := sqlc.New().ListEnabledEventSubscriptionsForAccount(ctx, s.pool, mustPgUUID(accountID))
	if err != nil {
		return nil, err
	}
	out := make([]EventSubscription, len(rows))
	for i, row := range rows {
		out[i] = eventSubscriptionFromSQL(row)
	}
	return out, nil
}

// UpsertEventSubscription installs a declaration idempotently. The boolean is
// true only when a new row was inserted, allowing deploy compensation to
// remove only rows created by the current operation.
func (s *PgStore) UpsertEventSubscription(ctx context.Context, accountID, appID, source, typ string, filter json.RawMessage) (EventSubscription, bool, error) {
	canonical, err := normalizeEventSubscriptionFilter(filter)
	if err != nil {
		return EventSubscription{}, false, err
	}
	var appOwned bool
	if err := s.pool.QueryRow(ctx,
		`select exists(select 1 from apps where id = $1 and account_id = $2 and status <> 'deleted')`,
		mustPgUUID(appID), mustPgUUID(accountID)).Scan(&appOwned); err != nil {
		return EventSubscription{}, false, err
	}
	if !appOwned {
		return EventSubscription{}, false, ErrNotFound
	}
	row, err := sqlc.New().UpsertEventSubscription(ctx, s.pool, sqlc.UpsertEventSubscriptionParams{
		AccountID: mustPgUUID(accountID),
		AppID:     mustPgUUID(appID),
		Source:    source,
		Type:      typ,
		Column5:   canonical,
	})
	if err != nil {
		return EventSubscription{}, false, err
	}
	return eventSubscriptionFromUpsert(row), row.Inserted, nil
}

func (s *PgStore) DeleteEventSubscription(ctx context.Context, id, accountID, appID string) error {
	return sqlc.New().DeleteEventSubscription(ctx, s.pool, sqlc.DeleteEventSubscriptionParams{
		ID:        mustPgUUID(id),
		AccountID: mustPgUUID(accountID),
		AppID:     mustPgUUID(appID),
	})
}

func eventSubscriptionKey(source, typ string, filter json.RawMessage) (string, error) {
	canonical, err := normalizeEventSubscriptionFilter(filter)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{source, typ, string(canonical)}, "\x00"), nil
}

func (m *MemStore) ListEventSubscriptionsForApp(_ context.Context, appID string) ([]EventSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	canonicalAppID := canonicalMemUUID(appID)
	out := make([]EventSubscription, 0)
	for _, subscription := range m.eventSubscriptions {
		if subscription.AppID == canonicalAppID {
			out = append(out, subscription)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) ListEnabledEventSubscriptionsForAccount(_ context.Context, accountID string) ([]EventSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	canonicalAccountID := canonicalMemUUID(accountID)
	out := make([]EventSubscription, 0)
	for _, subscription := range m.eventSubscriptions {
		app, appExists := m.apps[subscription.AppID]
		if subscription.AccountID == canonicalAccountID && subscription.Enabled && appExists && app.Status != AppDeleted {
			out = append(out, subscription)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) UpsertEventSubscription(_ context.Context, accountID, appID, source, typ string, filter json.RawMessage) (EventSubscription, bool, error) {
	canonical, err := normalizeEventSubscriptionFilter(filter)
	if err != nil {
		return EventSubscription{}, false, err
	}
	key, err := eventSubscriptionKey(source, typ, canonical)
	if err != nil {
		return EventSubscription{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, appExists := m.apps[canonicalMemUUID(appID)]
	if !appExists || app.AccountID != canonicalMemUUID(accountID) {
		return EventSubscription{}, false, ErrNotFound
	}
	if m.eventSubscriptions == nil {
		m.eventSubscriptions = make(map[string]EventSubscription)
	}
	for existingKey, subscription := range m.eventSubscriptions {
		if subscription.AppID == canonicalMemUUID(appID) && existingKey == key {
			return subscription, false, nil
		}
	}
	now := time.Now().UTC()
	subscription := EventSubscription{
		ID:        uuid.NewString(),
		AccountID: canonicalMemUUID(accountID),
		AppID:     canonicalMemUUID(appID),
		Source:    source,
		Type:      typ,
		Filter:    append(json.RawMessage(nil), canonical...),
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Include the app id in the map key so two apps can declare the same
	// source/type/filter without colliding in the in-memory implementation.
	m.eventSubscriptions[subscription.AppID+"\x00"+key] = subscription
	return subscription, true, nil
}

func (m *MemStore) DeleteEventSubscription(_ context.Context, id, accountID, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, subscription := range m.eventSubscriptions {
		if subscription.ID != id {
			continue
		}
		if subscription.AccountID != canonicalMemUUID(accountID) || subscription.AppID != canonicalMemUUID(appID) {
			return ErrNotFound
		}
		delete(m.eventSubscriptions, key)
		return nil
	}
	return ErrNotFound
}
