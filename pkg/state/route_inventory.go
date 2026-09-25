package state

import (
	"context"
	"sort"
	"strings"
	"time"
)

// DiscoveredRouteLimit bounds persistent per-app cardinality independently of
// the 50-label live metrics cap and the 30-day exact-audit retention period.
const DiscoveredRouteLimit = 500

const discoveredRouteOverflow = "__route_other__"

type usageEventIdentity struct{ accountID, appID string }

// APIRouteInventoryStore is a separate capability from exact request audit.
type APIRouteInventoryStore interface {
	ListDiscoveredAPIRoutes(context.Context, string, string, int) ([]DiscoveredAPIRoute, bool, error)
}

func discoveredRouteFor(event APIConsumerUsageEvent) string {
	if event.DiscoveredRoute != "" {
		return event.DiscoveredRoute
	}
	// Audit-enabled gateways predating the dedicated route field still have a
	// normalized candidate. This also preserves the initial audit API tests.
	if event.Audit != nil && event.Audit.RouteTemplate != discoveredRouteOverflow {
		return event.Audit.RouteTemplate
	}
	return ""
}

func discoveredAtFor(event APIConsumerUsageEvent) time.Time {
	if !event.DiscoveredAt.IsZero() {
		return event.DiscoveredAt.UTC()
	}
	if event.Audit != nil && !event.Audit.OccurredAt.IsZero() {
		return event.Audit.OccurredAt.UTC()
	}
	return event.WindowStart.UTC()
}

func discoveredRouteKey(accountID, appID, route string) string {
	return accountID + "\x00" + appID + "\x00" + route
}

func (m *MemStore) recordDiscoveredRouteLocked(event APIConsumerUsageEvent) {
	route := discoveredRouteFor(event)
	if route == "" {
		return
	}
	if _, already := m.discoveryReceipts[event.EventID]; already {
		return
	}
	prefix := discoveredRouteKey(event.AccountID, event.AppID, "")
	key := prefix + route
	if route != discoveredRouteOverflow {
		if _, exists := m.discoveredAPIRoutes[key]; !exists {
			count := 0
			for k := range m.discoveredAPIRoutes {
				if strings.HasPrefix(k, prefix) && k != prefix+discoveredRouteOverflow {
					count++
				}
			}
			if count >= DiscoveredRouteLimit {
				route = discoveredRouteOverflow
				key = prefix + route
			}
		}
	}
	when := discoveredAtFor(event)
	entry := m.discoveredAPIRoutes[key]
	if entry.RouteTemplate == "" {
		entry = DiscoveredAPIRoute{RouteTemplate: route, FirstSeen: when, LastSeen: when}
	}
	if when.Before(entry.FirstSeen) {
		entry.FirstSeen = when
	}
	if when.After(entry.LastSeen) {
		entry.LastSeen = when
	}
	entry.RequestCount++
	m.discoveredAPIRoutes[key] = entry
	m.discoveryReceipts[event.EventID] = struct{}{}
}

func (m *MemStore) ListDiscoveredAPIRoutes(_ context.Context, accountID, appID string, limit int) ([]DiscoveredAPIRoute, bool, error) {
	if err := validateAuditQuery(accountID, appID, limit); err != nil {
		return nil, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := discoveredRouteKey(accountID, appID, "")
	_, capHit := m.discoveredAPIRoutes[prefix+discoveredRouteOverflow]
	out := make([]DiscoveredAPIRoute, 0)
	for key, entry := range m.discoveredAPIRoutes {
		if strings.HasPrefix(key, prefix) && entry.RouteTemplate != discoveredRouteOverflow {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RouteTemplate < out[j].RouteTemplate })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, capHit, nil
}
