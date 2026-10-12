package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIUserStore = (*MemStore)(nil)

// GetOutboundAIUserUsage mirrors the PgStore read over seeded rollups: in
// development and tests nothing writes them unless SeedOutboundAIUserUsage
// is called.
func (m *MemStore) GetOutboundAIUserUsage(_ context.Context, accountID, integrationID, usageDate, endUser string, limit int) (OutboundAIUserUsage, error) {
	usageDate, ok := outboundAIUserUsageQuery(usageDate, endUser, time.Now())
	if !ok {
		return OutboundAIUserUsage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID {
		return OutboundAIUserUsage{}, ErrNotFound
	}
	rows := m.outboundAIUserUsage[integrationID+"/"+usageDate]
	usage := OutboundAIUserUsage{UsageDate: usageDate, TrackedUsers: int64(len(rows)), Items: make([]OutboundAIUserUsageItem, 0),
		UserDailyTokenLimit: copyInt64Ptr(offer.AIUserDailyTokenLimit), UserDailyCostLimit: copyInt64Ptr(offer.AIUserDailyCostLimit)}
	for _, row := range rows {
		if endUser == "" || row.EndUser == endUser {
			usage.Items = append(usage.Items, row)
		}
	}
	sort.SliceStable(usage.Items, func(i, j int) bool {
		a, b := usage.Items[i], usage.Items[j]
		if a.CostMillicents != b.CostMillicents {
			return a.CostMillicents > b.CostMillicents
		}
		if a.InputTokens+a.OutputTokens != b.InputTokens+b.OutputTokens {
			return a.InputTokens+a.OutputTokens > b.InputTokens+b.OutputTokens
		}
		return a.EndUser < b.EndUser
	})
	if page := boundedPage(limit, api.OutboundAIUserUsagePageMax); len(usage.Items) > page {
		usage.Items = usage.Items[:page]
	}
	return usage, nil
}

// SeedOutboundAIUserUsage stands in for outboundd's per-user rollup writes
// in tests.
func (m *MemStore) SeedOutboundAIUserUsage(integrationID, usageDate string, items ...OutboundAIUserUsageItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIUserUsage == nil {
		m.outboundAIUserUsage = make(map[string][]OutboundAIUserUsageItem)
	}
	key := integrationID + "/" + usageDate
	m.outboundAIUserUsage[key] = append(m.outboundAIUserUsage[key], items...)
}
