package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RoutePrioritySetting is an app's saved ADR-957 route priorities.
// Configured is false when nothing is saved.
type RoutePrioritySetting struct {
	Configured bool
	Routes     []api.RoutePriorityRule
	UpdatedAt  time.Time
}

// RoutePriorityStore persists route priorities; apid is the only writer.
type RoutePriorityStore interface {
	GetRoutePriorities(ctx context.Context, accountID, appID string) (RoutePrioritySetting, error)
	SetRoutePriorities(ctx context.Context, accountID, appID string, rules []api.RoutePriorityRule) (RoutePrioritySetting, error)
	DeleteRoutePriorities(ctx context.Context, accountID, appID string) error
}

type routePriorityReader interface {
	RoutePriorityStore
	GetRouteHealthGate(ctx context.Context, accountID, appID string) (api.RouteHealthGate, error)
}

// EffectiveRoutePriorities returns the rules the gateway applies: saved rules
// when present (even an empty list), otherwise the route-health selectors as
// critical routes.
func EffectiveRoutePriorities(ctx context.Context, store any, accountID, appID string) (string, RoutePrioritySetting, error) {
	reader, ok := store.(routePriorityReader)
	if !ok {
		return "", RoutePrioritySetting{}, errors.New("store does not support route priorities")
	}
	setting, err := reader.GetRoutePriorities(ctx, accountID, appID)
	if err != nil {
		return "", RoutePrioritySetting{}, err
	}
	if setting.Configured {
		return api.RoutePrioritySourceConfigured, setting, nil
	}
	gate, err := reader.GetRouteHealthGate(ctx, accountID, appID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", RoutePrioritySetting{}, err
	}
	out := RoutePrioritySetting{Routes: []api.RoutePriorityRule{}}
	for _, route := range gate.Routes {
		if _, ok := api.RoutePriorityGlob(route.Path); ok && len(out.Routes) < api.RoutePriorityMaxRules {
			out.Routes = append(out.Routes, api.RoutePriorityRule{Method: route.Method, Path: route.Path, Class: api.RoutePriorityCritical})
		}
	}
	if len(out.Routes) == 0 {
		return api.RoutePrioritySourceNone, out, nil
	}
	return api.RoutePrioritySourceRouteHealth, out, nil
}

func decodeRoutePriorities(raw []byte) ([]api.RoutePriorityRule, error) {
	rules := []api.RoutePriorityRule{}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("decode route priorities: %w", err)
	}
	return rules, nil
}

func (s *PgStore) GetRoutePriorities(ctx context.Context, accountID, appID string) (RoutePrioritySetting, error) {
	row, err := (&sqlc.Queries{}).GetRoutePriorities(ctx, s.pool, sqlc.GetRoutePrioritiesParams{AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return RoutePrioritySetting{Routes: []api.RoutePriorityRule{}}, nil
	}
	if err != nil {
		return RoutePrioritySetting{}, err
	}
	rules, err := decodeRoutePriorities(row.Routes)
	if err != nil {
		return RoutePrioritySetting{}, err
	}
	return RoutePrioritySetting{Configured: true, Routes: rules, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (s *PgStore) SetRoutePriorities(ctx context.Context, accountID, appID string, rules []api.RoutePriorityRule) (RoutePrioritySetting, error) {
	if err := api.ValidateRoutePriorities(rules); err != nil {
		return RoutePrioritySetting{}, err
	}
	if rules == nil {
		rules = []api.RoutePriorityRule{}
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return RoutePrioritySetting{}, err
	}
	row, err := (&sqlc.Queries{}).SetRoutePriorities(ctx, s.pool, sqlc.SetRoutePrioritiesParams{AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID), Routes: raw})
	if errors.Is(err, pgx.ErrNoRows) {
		return RoutePrioritySetting{}, ErrNotFound
	}
	if err != nil {
		return RoutePrioritySetting{}, err
	}
	saved, err := decodeRoutePriorities(row.Routes)
	if err != nil {
		return RoutePrioritySetting{}, err
	}
	return RoutePrioritySetting{Configured: true, Routes: saved, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (s *PgStore) DeleteRoutePriorities(ctx context.Context, accountID, appID string) error {
	return (&sqlc.Queries{}).DeleteRoutePriorities(ctx, s.pool, sqlc.DeleteRoutePrioritiesParams{AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID)})
}

type memRoutePriorities struct {
	accountID string
	setting   RoutePrioritySetting
}

func (m *MemStore) GetRoutePriorities(_ context.Context, accountID, appID string) (RoutePrioritySetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.routePriorities[appID]; ok && v.accountID == accountID {
		out := v.setting
		out.Routes = append([]api.RoutePriorityRule{}, v.setting.Routes...)
		return out, nil
	}
	return RoutePrioritySetting{Routes: []api.RoutePriorityRule{}}, nil
}

func (m *MemStore) SetRoutePriorities(_ context.Context, accountID, appID string, rules []api.RoutePriorityRule) (RoutePrioritySetting, error) {
	if err := api.ValidateRoutePriorities(rules); err != nil {
		return RoutePrioritySetting{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[appID]; !ok || app.AccountID != accountID {
		return RoutePrioritySetting{}, ErrNotFound
	}
	if m.routePriorities == nil {
		m.routePriorities = map[string]memRoutePriorities{}
	}
	setting := RoutePrioritySetting{Configured: true, Routes: append([]api.RoutePriorityRule{}, rules...), UpdatedAt: time.Now().UTC()}
	m.routePriorities[appID] = memRoutePriorities{accountID: accountID, setting: setting}
	setting.Routes = append([]api.RoutePriorityRule{}, setting.Routes...)
	return setting, nil
}

func (m *MemStore) DeleteRoutePriorities(_ context.Context, accountID, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.routePriorities[appID]; ok && v.accountID == accountID {
		delete(m.routePriorities, appID)
	}
	return nil
}
