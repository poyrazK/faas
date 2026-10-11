package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIStore = (*MemStore)(nil)

// SetOutboundAIPolicy mirrors the PgStore customer-only AI settings update.
func (m *MemStore) SetOutboundAIPolicy(_ context.Context, accountID, integrationID, provider string, dailyTokenLimit *int64) error {
	if !api.ValidOutboundAIPolicy(provider, dailyTokenLimit) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" || !offer.Enabled {
		return ErrNotFound
	}
	offer.AIProvider = provider
	offer.AIDailyTokenLimit = nil
	if dailyTokenLimit != nil {
		limit := *dailyTokenLimit
		offer.AIDailyTokenLimit = &limit
	}
	m.outboundIntegrationOffers[integrationID] = offer
	return nil
}

// GetOutboundAIUsage reports settings with no usage: in development and
// tests nothing writes rollups unless SeedOutboundAIUsage is called.
func (m *MemStore) GetOutboundAIUsage(_ context.Context, accountID, integrationID, usageDate string) (OutboundAIUsage, error) {
	now := time.Now().UTC()
	if usageDate == "" {
		usageDate = now.Format("2006-01-02")
	}
	day, ok := validOutboundAIUsageDate(usageDate, now)
	if !ok {
		return OutboundAIUsage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID {
		return OutboundAIUsage{}, ErrNotFound
	}
	usage := OutboundAIUsage{
		UsageDate: usageDate, AIProvider: offer.AIProvider, ResetsAt: day.AddDate(0, 0, 1),
		Items: append([]OutboundAIUsageItem{}, m.outboundAIUsage[integrationID+"/"+usageDate]...),
	}
	if offer.AIDailyTokenLimit != nil {
		limit := *offer.AIDailyTokenLimit
		usage.DailyTokenLimit = &limit
	}
	for _, item := range usage.Items {
		usage.DailyTokenCount += item.InputTokens + item.OutputTokens
	}
	return usage, nil
}

// SeedOutboundAIUsage stands in for outboundd's rollup writes in tests.
func (m *MemStore) SeedOutboundAIUsage(integrationID, usageDate string, items ...OutboundAIUsageItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIUsage == nil {
		m.outboundAIUsage = make(map[string][]OutboundAIUsageItem)
	}
	key := integrationID + "/" + usageDate
	m.outboundAIUsage[key] = append(m.outboundAIUsage[key], items...)
}
