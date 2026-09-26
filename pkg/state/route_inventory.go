package state

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DiscoveredRouteLimit bounds persistent per-app cardinality independently of
// the 50-label live metrics cap and the 30-day exact-audit retention period.
const DiscoveredRouteLimit = 500

const discoveredRouteOverflow = "__route_other__"

const (
	discoveredRouteEventSource = "gregale.api"
	discoveredRouteEventType   = "com.gregale.api.route.discovered"
)

type discoveredRouteEventData struct {
	AppID         string    `json:"app_id"`
	RouteTemplate string    `json:"route_template"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	FirstSeen     time.Time `json:"first_seen"`
}

type discoveredRouteCloudEvent struct {
	SpecVersion     string                   `json:"specversion"`
	ID              string                   `json:"id"`
	Source          string                   `json:"source"`
	Type            string                   `json:"type"`
	Time            time.Time                `json:"time"`
	DataContentType string                   `json:"data_content_type"`
	Data            discoveredRouteEventData `json:"data"`
	AccountID       string                   `json:"account_id"`
}

type discoveredRouteNotice struct {
	AccountID     string    `json:"account_id"`
	AppID         string    `json:"app_id"`
	RouteTemplate string    `json:"route_template"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	FirstSeen     time.Time `json:"first_seen"`
}

// apiRouteDiscoveredPayloads returns the canonical event-fanout envelope and
// the smaller account-scoped SSE notice for one first-seen route. The event
// ID is stable for the account/app/template so fanout retries stay idempotent.
func apiRouteDiscoveredPayloads(accountID, appID, route string, firstSeen time.Time) (eventPayload, noticePayload []byte, err error) {
	method, path, ok := strings.Cut(strings.TrimSpace(route), " ")
	if !ok || method == "" || path == "" {
		return nil, nil, nil
	}
	firstSeen = firstSeen.UTC()
	eventData := discoveredRouteEventData{
		AppID: appID, RouteTemplate: route, Method: method, Path: path, FirstSeen: firstSeen,
	}
	eventID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:api-route-discovered\x00"+accountID+"\x00"+appID+"\x00"+route)).String()
	envelope := discoveredRouteCloudEvent{
		SpecVersion: "1.0", ID: eventID, Source: discoveredRouteEventSource,
		Type: discoveredRouteEventType, Time: firstSeen, DataContentType: "application/json",
		Data: eventData, AccountID: accountID,
	}
	eventPayload, err = json.Marshal(envelope)
	if err != nil {
		return nil, nil, err
	}
	noticePayload, err = json.Marshal(discoveredRouteNotice{
		AccountID: accountID, AppID: appID, RouteTemplate: route,
		Method: method, Path: path, FirstSeen: firstSeen,
	})
	return eventPayload, noticePayload, err
}

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

func (m *MemStore) recordDiscoveredRouteLocked(event APIConsumerUsageEvent) []byte {
	route := discoveredRouteFor(event)
	if route == "" {
		return nil
	}
	if _, already := m.discoveryReceipts[event.EventID]; already {
		return nil
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
	newRoute := entry.RouteTemplate == "" && route != discoveredRouteOverflow
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
	if !newRoute {
		return nil
	}
	eventPayload, _, err := apiRouteDiscoveredPayloads(event.AccountID, event.AppID, route, when)
	if err != nil {
		return nil
	}
	return eventPayload
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
