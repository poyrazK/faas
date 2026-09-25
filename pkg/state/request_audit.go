package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// RequestAuditStore is deliberately additive to Store: the audit rollout does
// not require unrelated test stores to implement this read surface.
type RequestAuditStore interface {
	ListRequestAudit(context.Context, string, string, time.Time, time.Time, int) ([]RequestAuditRecord, error)
	ListDiscoveredAuditRoutes(context.Context, string, string, int) ([]string, error)
}

func (m *MemStore) recordRequestAuditLocked(event APIConsumerUsageEvent) {
	if m.requestAuditEvents == nil {
		m.requestAuditEvents = make(map[string]RequestAuditRecord)
	}
	if _, exists := m.requestAuditEvents[event.EventID]; exists {
		return
	}
	consumerID := event.ConsumerKey
	if consumerID == AnonymousConsumerKey {
		consumerID = ""
	}
	m.requestAuditEvents[event.EventID] = RequestAuditRecord{
		EventID: event.EventID, AccountID: event.AccountID, AppID: event.AppID,
		ConsumerID: consumerID, PlatformTenantID: event.PlatformTenantID,
		RequestAuditEvidence: *event.Audit,
	}
}

func (m *MemStore) ListRequestAudit(_ context.Context, accountID, appID string, since, until time.Time, limit int) ([]RequestAuditRecord, error) {
	if err := validateAuditQuery(accountID, appID, limit); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RequestAuditRecord, 0)
	for _, record := range m.requestAuditEvents {
		if record.AccountID == accountID && record.AppID == appID && !record.OccurredAt.Before(since) && record.OccurredAt.Before(until) {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OccurredAt.Equal(out[j].OccurredAt) {
			return out[i].EventID > out[j].EventID
		}
		return out[i].OccurredAt.After(out[j].OccurredAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ListDiscoveredAuditRoutes(_ context.Context, accountID, appID string, limit int) ([]string, error) {
	if err := validateAuditQuery(accountID, appID, limit); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]struct{})
	for _, record := range m.requestAuditEvents {
		if record.AccountID == accountID && record.AppID == appID && record.RouteTemplate != "__route_other__" {
			seen[record.RouteTemplate] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for route := range seen {
		out = append(out, route)
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func validateAuditQuery(accountID, appID string, limit int) error {
	if _, err := uuid.Parse(accountID); err != nil {
		return fmt.Errorf("request audit: invalid account ID: %w", err)
	}
	if _, err := uuid.Parse(appID); err != nil {
		return fmt.Errorf("request audit: invalid app ID: %w", err)
	}
	if limit < 1 || limit > 500 {
		return fmt.Errorf("request audit: limit must be 1..500")
	}
	return nil
}
