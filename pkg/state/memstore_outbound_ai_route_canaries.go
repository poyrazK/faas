package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The memory store keeps canaries on their routes. It has no settlements,
// so a canary's counts are always zero.

func (m *MemStore) SetOutboundAIRouteCanary(_ context.Context, accountID, routeID string, canary OutboundAIRouteCanary) (OutboundAIRouteCanary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[routeID]
	if !ok || route.AccountID != accountID {
		return OutboundAIRouteCanary{}, ErrNotFound
	}
	if !validOutboundAIRouteCanary(canary, len(route.Targets)) {
		return OutboundAIRouteCanary{}, ErrInvalidArgument
	}
	canary.State, canary.Reason, canary.DecidedAt, canary.StartedAt = api.OutboundAIRouteCanaryActive, "", nil, time.Now().UTC()
	route.Canary = &canary
	m.outboundAIRoutes[routeID] = copyOutboundAIRoute(route)
	return *copyOutboundAIRoute(route).Canary, nil
}

func (m *MemStore) DeleteOutboundAIRouteCanary(_ context.Context, accountID, routeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[routeID]
	if !ok || route.AccountID != accountID || route.Canary == nil {
		return ErrNotFound
	}
	route.Canary = nil
	m.outboundAIRoutes[routeID] = route
	return nil
}

func (m *MemStore) PromoteOutboundAIRouteCanary(_ context.Context, accountID, routeID string) (OutboundAIRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[routeID]
	if !ok || route.AccountID != accountID || route.Canary == nil {
		return OutboundAIRoute{}, ErrNotFound
	}
	if route.Canary.Position > len(route.Targets) {
		return OutboundAIRoute{}, ErrInvalidArgument
	}
	route.Targets = promotedOutboundAIRouteTargets(route.Targets, route.Canary.Position)
	route.Canary = nil
	route.UpdatedAt = time.Now().UTC()
	m.outboundAIRoutes[routeID] = route
	return copyOutboundAIRoute(route), nil
}

func (m *MemStore) OutboundAIRouteCanaryStatus(_ context.Context, accountID, routeID string) (OutboundAIRouteCanaryStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[routeID]
	if !ok || route.AccountID != accountID || route.Canary == nil {
		return OutboundAIRouteCanaryStatus{}, ErrNotFound
	}
	canary := *copyOutboundAIRoute(route).Canary
	windowStart := time.Now().UTC().Add(-time.Duration(canary.WindowMinutes) * time.Minute)
	if canary.StartedAt.After(windowStart) {
		windowStart = canary.StartedAt
	}
	return OutboundAIRouteCanaryStatus{Canary: canary, WindowStart: windowStart,
		Baseline: OutboundAIRouteTargetStats{Position: 1}, CanaryStats: OutboundAIRouteTargetStats{Position: canary.Position}}, nil
}
