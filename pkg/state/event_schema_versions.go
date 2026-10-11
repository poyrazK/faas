package state

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventSubscriptionSchemaVersionsStore interface {
	GetEventSubscriptionSchemaVersions(context.Context, string, string, string) (api.EventSubscriptionSchemaVersionsResponse, error)
	SetEventSubscriptionSchemaVersions(context.Context, string, string, string, []string, ...[]string) ([]string, error)
}

func eventSchemaVersionsResponse(id string, versions []string) api.EventSubscriptionSchemaVersionsResponse {
	return api.EventSubscriptionSchemaVersionsResponse{SubscriptionID: canonicalMemUUID(id), SchemaVersions: append([]string{}, versions...)}
}
func (s *PgStore) GetEventSubscriptionSchemaVersions(ctx context.Context, account, app, id string) (api.EventSubscriptionSchemaVersionsResponse, error) {
	if err := validSubscriptionControlIDs(account, app, id); err != nil {
		return api.EventSubscriptionSchemaVersionsResponse{}, err
	}
	row, err := sqlc.New().EventRoutingRetryTarget(ctx, s.pool, sqlc.EventRoutingRetryTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(id)})
	if err != nil {
		return api.EventSubscriptionSchemaVersionsResponse{}, mapErr(err)
	}
	return eventSchemaVersionsResponse(id, row.SchemaVersions), nil
}
func (s *PgStore) SetEventSubscriptionSchemaVersions(ctx context.Context, account, app, id string, versions []string, expected ...[]string) ([]string, error) {
	if err := validSubscriptionControlIDs(account, app, id); err != nil {
		return nil, err
	}
	normalized, err := api.NormalizeEventSchemaVersions(versions)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.EventRoutingRetryLockTarget(ctx, tx, sqlc.EventRoutingRetryLockTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(id)})
	if err != nil {
		return nil, mapErr(err)
	}
	if len(expected) > 0 && !slices.Equal(row.SchemaVersions, expected[0]) {
		return nil, ErrConflict
	}
	old := append([]string(nil), row.SchemaVersions...)
	if !slices.Equal(old, normalized) {
		if err = q.EventSubscriptionSchemaVersionsSet(ctx, tx, sqlc.EventSubscriptionSchemaVersionsSetParams{SubscriptionID: mustPgUUID(id), SchemaVersions: append([]string{}, normalized...)}); err != nil {
			return nil, err
		}
	}
	return old, tx.Commit(ctx)
}
func (m *MemStore) GetEventSubscriptionSchemaVersions(ctx context.Context, account, app, id string) (api.EventSubscriptionSchemaVersionsResponse, error) {
	if err := ctx.Err(); err != nil {
		return api.EventSubscriptionSchemaVersionsResponse{}, err
	}
	if err := validSubscriptionControlIDs(account, app, id); err != nil {
		return api.EventSubscriptionSchemaVersionsResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.eventRoutingRetryTargetLocked(account, app, id)
	if row == nil {
		return api.EventSubscriptionSchemaVersionsResponse{}, ErrNotFound
	}
	return eventSchemaVersionsResponse(id, row.SchemaVersions), nil
}
func (m *MemStore) SetEventSubscriptionSchemaVersions(ctx context.Context, account, app, id string, versions []string, expected ...[]string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validSubscriptionControlIDs(account, app, id); err != nil {
		return nil, err
	}
	normalized, err := api.NormalizeEventSchemaVersions(versions)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.eventRoutingRetryTargetLocked(account, app, id)
	if row == nil {
		return nil, ErrNotFound
	}
	if len(expected) > 0 && !slices.Equal(row.SchemaVersions, expected[0]) {
		return nil, ErrConflict
	}
	old := append([]string(nil), row.SchemaVersions...)
	if !slices.Equal(old, normalized) {
		row.SchemaVersions = normalized
		row.UpdatedAt = time.Now().UTC()
		for key, v := range m.eventSubscriptions {
			if sameMemUUID(v.ID, id) {
				m.eventSubscriptions[key] = *row
				break
			}
		}
	}
	return old, nil
}

var _ EventSubscriptionSchemaVersionsStore = (*PgStore)(nil)
var _ EventSubscriptionSchemaVersionsStore = (*MemStore)(nil)
