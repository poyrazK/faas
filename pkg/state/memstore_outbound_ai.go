package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIStore = (*MemStore)(nil)

func copyInt64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	value := *in
	return &value
}

// SetOutboundAIPolicy mirrors the PgStore customer-only AI settings update.
func (m *MemStore) SetOutboundAIPolicy(_ context.Context, accountID, integrationID string, policy OutboundAIPolicy) error {
	if !policy.valid() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" || !offer.Enabled {
		return ErrNotFound
	}
	offer.AIProvider = policy.Provider
	offer.AIDailyTokenLimit = copyInt64Ptr(policy.DailyTokenLimit)
	offer.AIDailyCostLimit = copyInt64Ptr(policy.DailyCostLimit)
	offer.AIMonthlyCostLimit = copyInt64Ptr(policy.MonthlyCostLimit)
	offer.AIRequestLogEnabled = policy.RequestLogEnabled
	offer.AIRequestLogContent = policy.RequestLogContent
	offer.AILogExport = policy.LogExport
	offer.AIAllowUnmetered = policy.AllowUnmetered
	offer.AIAutoPromptCache = policy.AutoPromptCache
	if policy.Provider == "" {
		offer.AIContentPolicy = api.OutboundAIContentPolicy{}
	}
	offer.AIUserDailyTokenLimit = copyInt64Ptr(policy.UserDailyTokenLimit)
	offer.AIUserDailyCostLimit = copyInt64Ptr(policy.UserDailyCostLimit)
	offer.AITraceMaxCalls = copyInt64Ptr(policy.TraceMaxCalls)
	offer.AITraceMaxCostMillicents = copyInt64Ptr(policy.TraceMaxCost)
	offer.AITraceMaxRepeatedToolCalls = copyInt64Ptr(policy.TraceMaxRepeatedToolCalls)
	m.outboundIntegrationOffers[integrationID] = offer
	return nil
}

// GetOutboundAIUsage reports settings with seeded rollups: in development and
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
		UsageDate: usageDate, AIProvider: offer.AIProvider, ResetsAt: day.AddDate(0, 0, 1), Month: day.Format("2006-01"),
		Items:           append([]OutboundAIUsageItem{}, m.outboundAIUsage[integrationID+"/"+usageDate]...),
		DailyTokenLimit: copyInt64Ptr(offer.AIDailyTokenLimit), DailyCostLimit: copyInt64Ptr(offer.AIDailyCostLimit),
		MonthlyCostLimit: copyInt64Ptr(offer.AIMonthlyCostLimit),
	}
	for _, item := range usage.Items {
		usage.DailyTokenCount += item.InputTokens + item.OutputTokens
		usage.DailyCostMillicents += item.CostMillicents
	}
	for monthDay := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC); !monthDay.After(day); monthDay = monthDay.AddDate(0, 0, 1) {
		for _, item := range m.outboundAIUsage[integrationID+"/"+monthDay.Format("2006-01-02")] {
			usage.MonthlyCostMillicents += item.CostMillicents
		}
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

// SetOutboundBindingAITokenLimit mirrors the PgStore per-app limit update.
func (m *MemStore) SetOutboundBindingAITokenLimit(_ context.Context, accountID, appID, integrationID string, limit *int64) error {
	if limit != nil && (*limit < 1 || *limit > api.MaxOutboundAITokensPerDay) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.AIProvider == "" {
		return ErrNotFound
	}
	key := appID + "|" + integrationID
	binding, ok := m.outboundAppBindings[key]
	if !ok || binding.AccountID != accountID {
		return ErrNotFound
	}
	binding.BindingAIDailyTokenLimit = copyInt64Ptr(limit)
	m.outboundAppBindings[key] = binding
	return nil
}

// GetOutboundAIPrices returns the newest price list set through the store.
func (m *MemStore) GetOutboundAIPrices(_ context.Context, accountID, integrationID string) (OutboundAIPriceList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID {
		return OutboundAIPriceList{}, ErrNotFound
	}
	list, ok := m.outboundAIPrices[integrationID]
	if !ok {
		return OutboundAIPriceList{Entries: make([]api.OutboundAIPriceEntry, 0)}, nil
	}
	effective := *list.EffectiveFrom
	return OutboundAIPriceList{EffectiveFrom: &effective, Entries: append([]api.OutboundAIPriceEntry{}, list.Entries...)}, nil
}

// SetOutboundAIPrices replaces the price list for a customer integration.
func (m *MemStore) SetOutboundAIPrices(_ context.Context, accountID, integrationID string, entries []api.OutboundAIPriceEntry) error {
	if !api.ValidOutboundAIPriceEntries(entries) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" {
		return ErrNotFound
	}
	if m.outboundAIPrices == nil {
		m.outboundAIPrices = make(map[string]OutboundAIPriceList)
	}
	now := time.Now().UTC()
	m.outboundAIPrices[integrationID] = OutboundAIPriceList{EffectiveFrom: &now, Entries: append([]api.OutboundAIPriceEntry{}, entries...)}
	return nil
}

var _ OutboundAIContentPolicyStore = (*MemStore)(nil)

// SetOutboundAIContentPolicy mirrors the PgStore content policy update
// (ADR-997).
func (m *MemStore) SetOutboundAIContentPolicy(_ context.Context, accountID, integrationID string, policy api.OutboundAIContentPolicy) error {
	if !policy.Valid() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.OwnerKind != "customer" || !offer.Enabled {
		return ErrNotFound
	}
	if (policy.Active() || policy.Moderation.Active() || policy.Response.Active()) && offer.AIProvider == "" {
		return ErrInvalidArgument
	}
	policy = policy.Normalized()
	if policy.Moderation.Active() || policy.Response.Moderation != api.OutboundAIModerationOff {
		moderator, ok := m.outboundIntegrationOffers[policy.Moderation.IntegrationID]
		if !ok || moderator.AccountID != accountID || moderator.OwnerKind != "customer" || !moderator.Enabled || moderator.AIProvider == "" ||
			(policy.Moderation.Kind == api.OutboundAIModerationKindOpenAI && moderator.AIProvider != api.OutboundAIProviderOpenAI) {
			return ErrInvalidArgument
		}
		policy.Moderation.Categories = append([]string{}, policy.Moderation.Categories...)
	}
	policy.BlockedTerms = append([]string{}, policy.BlockedTerms...)
	offer.AIContentPolicy = policy
	m.outboundIntegrationOffers[integrationID] = offer
	return nil
}
