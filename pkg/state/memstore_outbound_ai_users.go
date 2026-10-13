package state

import (
	"context"
	"sort"
	"strings"
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

// ExportOutboundAIUserUsage mirrors the PgStore export over seeded rollups.
func (m *MemStore) ExportOutboundAIUserUsage(_ context.Context, accountID, integrationID, from, to string,
	visit func(OutboundAIUserUsageRow) error) error {
	from, to, ok := outboundAIUserExportRange(from, to, time.Now())
	if !ok {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	offer, found := m.outboundIntegrationOffers[integrationID]
	var rows []OutboundAIUserUsageRow
	if found && offer.AccountID == accountID {
		for key, items := range m.outboundAIUserUsage {
			id, day, _ := strings.Cut(key, "/")
			if id != integrationID || day < from || day > to {
				continue
			}
			for _, item := range items {
				rows = append(rows, OutboundAIUserUsageRow{UsageDate: day, OutboundAIUserUsageItem: item})
			}
		}
	}
	m.mu.Unlock()
	if !found || offer.AccountID != accountID {
		return ErrNotFound
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UsageDate != rows[j].UsageDate {
			return rows[i].UsageDate < rows[j].UsageDate
		}
		return rows[i].EndUser < rows[j].EndUser
	})
	for _, row := range rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
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
