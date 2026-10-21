package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIQualityStore = (*MemStore)(nil)

// ListOutboundAIQualityAlerts mirrors the PgStore listing over seeded
// alerts.
func (m *MemStore) ListOutboundAIQualityAlerts(_ context.Context, accountID, integrationID string, limit int) ([]OutboundAIQualityAlert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return nil, ErrNotFound
	}
	alerts := append(make([]OutboundAIQualityAlert, 0), m.outboundAIQualityAlerts[integrationID]...)
	sort.SliceStable(alerts, func(i, j int) bool { return alerts[i].DetectedAt.After(alerts[j].DetectedAt) })
	if page := boundedPage(limit, api.OutboundAIQualityAlertsPageMax); len(alerts) > page {
		alerts = alerts[:page]
	}
	return alerts, nil
}

// SetOutboundAIRequestFeedback mirrors the PgStore rating of a seeded
// request-log entry; the memory store keeps no daily rollup to move.
func (m *MemStore) SetOutboundAIRequestFeedback(_ context.Context, accountID, integrationID, requestID, rating, label string) error {
	if !api.ValidOutboundAIFeedback(rating, label) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return ErrNotFound
	}
	entries := m.outboundAIRequestLog[integrationID]
	for index := range entries {
		if entries[index].ID == requestID {
			entries[index].Feedback, entries[index].FeedbackLabel = rating, label
			return nil
		}
	}
	return ErrNotFound
}

// SeedOutboundAIQualityAlert stands in for outboundd's drift alerts in
// tests.
func (m *MemStore) SeedOutboundAIQualityAlert(integrationID string, alerts ...OutboundAIQualityAlert) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIQualityAlerts == nil {
		m.outboundAIQualityAlerts = make(map[string][]OutboundAIQualityAlert)
	}
	m.outboundAIQualityAlerts[integrationID] = append(m.outboundAIQualityAlerts[integrationID], alerts...)
}
