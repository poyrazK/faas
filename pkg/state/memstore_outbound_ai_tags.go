package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAITagStore = (*MemStore)(nil)

func (m *MemStore) GetOutboundAITagUsage(_ context.Context, accountID, integrationID, usageDate, key string, limit int) (OutboundAITagUsage, error) {
	usageDate, ok := outboundAITagUsageQuery(usageDate, key, time.Now())
	if !ok {
		return OutboundAITagUsage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return OutboundAITagUsage{}, ErrNotFound
	}
	rows := m.outboundAITagUsage[integrationID+"/"+usageDate]
	usage := OutboundAITagUsage{UsageDate: usageDate, TrackedTags: int64(len(rows)), Items: make([]OutboundAITagUsageItem, 0)}
	limits := make(map[string]int64)
	for _, tagLimit := range m.outboundAITagLimits[integrationID] {
		limits[tagLimit.Tag] = tagLimit.DailyCostLimitMillicents
	}
	for _, row := range rows {
		if !outboundAITagKeyMatches(row.Tag, key) {
			continue
		}
		if value, ok := limits[row.Tag]; ok {
			row.DailyCostLimit = &value
		}
		usage.Items = append(usage.Items, row)
	}
	sort.SliceStable(usage.Items, func(i, j int) bool {
		a, b := usage.Items[i], usage.Items[j]
		if a.CostMillicents != b.CostMillicents {
			return a.CostMillicents > b.CostMillicents
		}
		if a.InputTokens+a.OutputTokens != b.InputTokens+b.OutputTokens {
			return a.InputTokens+a.OutputTokens > b.InputTokens+b.OutputTokens
		}
		return a.Tag < b.Tag
	})
	usage.Items = usage.Items[:min(len(usage.Items), boundedPage(limit, api.OutboundAITagUsagePageMax))]
	return usage, nil
}

func (m *MemStore) ListOutboundAITagLimits(_ context.Context, accountID, integrationID string) ([]api.OutboundAITagLimit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return nil, ErrNotFound
	}
	return sortedOutboundAITagLimits(m.outboundAITagLimits[integrationID]), nil
}

func (m *MemStore) SetOutboundAITagLimits(_ context.Context, accountID, integrationID string, limits []api.OutboundAITagLimit) error {
	if !api.ValidOutboundAITagLimits(limits) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID {
		return ErrNotFound
	}
	if offer.AIProvider == "" && len(limits) > 0 {
		return ErrInvalidArgument
	}
	if m.outboundAITagLimits == nil {
		m.outboundAITagLimits = make(map[string][]api.OutboundAITagLimit)
	}
	m.outboundAITagLimits[integrationID] = sortedOutboundAITagLimits(limits)
	return nil
}

// SeedOutboundAITagUsage stands in for outboundd's per-tag rollup writes in
// tests.
func (m *MemStore) SeedOutboundAITagUsage(integrationID, usageDate string, items ...OutboundAITagUsageItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAITagUsage == nil {
		m.outboundAITagUsage = make(map[string][]OutboundAITagUsageItem)
	}
	key := integrationID + "/" + usageDate
	m.outboundAITagUsage[key] = append(m.outboundAITagUsage[key], items...)
}

func (m *MemStore) ExportOutboundAITagUsage(_ context.Context, accountID, integrationID, from, to, key string,
	visit func(OutboundAITagUsageRow) error) error {
	from, to, ok := outboundAITagExportRange(from, to, key, time.Now())
	if !ok {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	owned := m.memOutboundIntegrationOwned(accountID, integrationID)
	var rows []OutboundAITagUsageRow
	for storeKey, items := range m.outboundAITagUsage {
		id, day, _ := strings.Cut(storeKey, "/")
		if !owned || id != integrationID || day < from || day > to {
			continue
		}
		for _, item := range items {
			if outboundAITagKeyMatches(item.Tag, key) {
				rows = append(rows, OutboundAITagUsageRow{UsageDate: day, OutboundAITagUsageItem: item})
			}
		}
	}
	m.mu.Unlock()
	if !owned {
		return ErrNotFound
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UsageDate != rows[j].UsageDate {
			return rows[i].UsageDate < rows[j].UsageDate
		}
		return rows[i].Tag < rows[j].Tag
	})
	for _, row := range rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}
