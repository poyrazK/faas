package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIRouteStore = (*MemStore)(nil)

func (m *MemStore) memOutboundAIProviders(accountID string, targets []OutboundAIRouteTarget) map[string]outboundAIRouteTarget {
	providers := make(map[string]outboundAIRouteTarget, len(targets))
	for _, target := range targets {
		offer, ok := m.outboundIntegrationOffers[target.IntegrationID]
		if ok && offer.AccountID == accountID && offer.Enabled {
			providers[target.IntegrationID] = outboundAIRouteTarget{provider: offer.AIProvider,
				pathPrefixes: append([]string(nil), offer.AllowedPathPrefixes...)}
		}
	}
	return providers
}

func (m *MemStore) ListOutboundAIRoutes(_ context.Context, accountID string) ([]OutboundAIRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundAIRoute, 0)
	for _, route := range m.outboundAIRoutes {
		if route.AccountID == accountID {
			out = append(out, copyOutboundAIRoute(route))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) GetOutboundAIRoute(_ context.Context, accountID, routeID string) (OutboundAIRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[routeID]
	if !ok || route.AccountID != accountID {
		return OutboundAIRoute{}, ErrNotFound
	}
	return copyOutboundAIRoute(route), nil
}

func (m *MemStore) CreateOutboundAIRoute(_ context.Context, route OutboundAIRoute) (OutboundAIRoute, error) {
	if !api.ValidOutboundAIRouteName(route.Name) || !validOutboundAIRouteShape(route.Targets) {
		return OutboundAIRoute{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIRoutes == nil {
		m.outboundAIRoutes = make(map[string]OutboundAIRoute)
	}
	count := 0
	for _, existing := range m.outboundAIRoutes {
		if existing.AccountID != route.AccountID {
			continue
		}
		if existing.Name == route.Name {
			return OutboundAIRoute{}, ErrConflict
		}
		count++
	}
	if count >= api.MaxOutboundAIRoutesPerAccount {
		return OutboundAIRoute{}, ErrOutboundAIRouteLimit
	}
	format, ok := normalizeOutboundAIRouteFormat(route.Format)
	routing, routingOK := normalizeOutboundAIRouteRouting(OutboundAIRouteRouting{Strategy: route.Strategy, MaxTTFBMS: route.MaxTTFBMS}, route.Targets)
	if !ok || !routingOK || !outboundAIRouteTargetsAllowed(format, route.Targets, m.memOutboundAIProviders(route.AccountID, route.Targets)) {
		return OutboundAIRoute{}, ErrInvalidArgument
	}
	route.Format, route.Strategy, route.MaxTTFBMS = format, routing.Strategy, routing.MaxTTFBMS
	now := time.Now().UTC()
	route.CreatedAt, route.UpdatedAt = now, now
	m.outboundAIRoutes[route.ID] = copyOutboundAIRoute(route)
	return copyOutboundAIRoute(route), nil
}

func (m *MemStore) SetOutboundAIRouteTargets(_ context.Context, accountID, routeID string, targets []OutboundAIRouteTarget,
	routing OutboundAIRouteRouting) error {
	routing, routingOK := normalizeOutboundAIRouteRouting(routing, targets)
	if !routingOK || !validOutboundAIRouteShape(targets) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[routeID]
	if !ok || route.AccountID != accountID {
		return ErrNotFound
	}
	if !outboundAIRouteTargetsAllowed(route.Format, targets, m.memOutboundAIProviders(accountID, targets)) {
		return ErrInvalidArgument
	}
	route.Targets = append([]OutboundAIRouteTarget(nil), targets...)
	route.Canary = nil
	route.Strategy, route.MaxTTFBMS = routing.Strategy, routing.MaxTTFBMS
	route.UpdatedAt = time.Now().UTC()
	m.outboundAIRoutes[routeID] = copyOutboundAIRoute(route)
	return nil
}

// OutboundAIRouteStats reports no attempts: the memory store keeps no
// per-minute rows (ADR-1434).
func (m *MemStore) OutboundAIRouteStats(_ context.Context, accountID, routeID string) (OutboundAIRouteStatsWindow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[routeID]
	if !ok || route.AccountID != accountID {
		return OutboundAIRouteStatsWindow{}, ErrNotFound
	}
	window := OutboundAIRouteStatsWindow{Strategy: route.Strategy,
		WindowStart: time.Now().UTC().Add(-api.OutboundAIRouteStatsWindowMinutes * time.Minute).Truncate(time.Minute),
		Targets:     make([]OutboundAIRouteTargetStats, len(route.Targets))}
	for index := range window.Targets {
		window.Targets[index].Position = index + 1
	}
	return window, nil
}

func (m *MemStore) DeleteOutboundAIRoute(_ context.Context, accountID, routeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[routeID]
	if !ok || route.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.outboundAIRoutes, routeID)
	return nil
}
