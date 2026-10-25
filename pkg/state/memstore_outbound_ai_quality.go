package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	_ OutboundAIQualityStore   = (*MemStore)(nil)
	_ OutboundAICallStatsStore = (*MemStore)(nil)
)

// GetOutboundAICallStats mirrors the PgStore summary over seeded
// request-log entries, with nearest-rank percentiles.
func (m *MemStore) GetOutboundAICallStats(_ context.Context, accountID, integrationID string) ([]OutboundAICallStats, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return nil, 0, ErrNotFound
	}
	since := time.Now().Add(-api.OutboundAICallStatsWindowHours * time.Hour)
	entries := make([]OutboundAIRequestLogEntry, 0)
	for _, entry := range m.outboundAIRequestLog[integrationID] {
		if !entry.CreatedAt.Before(since) {
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
	if len(entries) > api.OutboundAICallStatsCallsMax {
		entries = entries[:api.OutboundAICallStatsCallsMax]
	}
	byModel := map[string][]OutboundAIRequestLogEntry{}
	for _, entry := range entries {
		byModel[entry.Model] = append(byModel[entry.Model], entry)
	}
	stats := make([]OutboundAICallStats, 0, len(byModel))
	for model, calls := range byModel {
		item := OutboundAICallStats{Model: model, Calls: int64(len(calls))}
		var ttfb, duration []int64
		for _, call := range calls {
			if call.Outcome == "error" || call.Status >= 400 {
				item.ErrorCalls++
			}
			if call.TTFBMS != nil {
				ttfb = append(ttfb, *call.TTFBMS)
			}
			duration = append(duration, call.DurationMS)
		}
		item.TTFBP50MS, item.TTFBP95MS = memPercentile(ttfb, 50), memPercentile(ttfb, 95)
		item.DurationP50MS, item.DurationP95MS = memPercentile(duration, 50), memPercentile(duration, 95)
		stats = append(stats, item)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Calls != stats[j].Calls {
			return stats[i].Calls > stats[j].Calls
		}
		return stats[i].Model < stats[j].Model
	})
	return stats, int64(len(entries)), nil
}

func memPercentile(values []int64, percent int) *int64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	value := sorted[(len(sorted)-1)*percent/100]
	return &value
}

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
