package outbound

// adr: 1440 — Postgres settles a cache hit as a cached request with the
// cost it avoided and no tokens or cost, and a provider call with what
// prompt caching saved.

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresCacheSavings(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []AIUsageRecord{
		{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown, CostMillicents: 50, PromptCacheSavedMillicents: -7,
			Usage: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 20, CacheWriteTokens: 15, OutputTokens: 10}},
		{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown, CostMillicents: 20, PromptCacheSavedMillicents: 30,
			Usage: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 20, CachedInputTokens: 15, OutputTokens: 10}},
		{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown, Cached: true, CacheSavedMillicents: 50,
			Usage: AIUsage{Model: "gpt-4o", Known: true}},
	} {
		if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := backend.RecordAIUsageAlerts(ctx, AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown,
		Cached: true, CacheSavedMillicents: 5, CostMillicents: 1, Usage: AIUsage{Model: "gpt-4o", Known: true}}); err == nil {
		t.Fatal("a cache hit with a cost was settled")
	}
	usage, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, "")
	if err != nil || len(usage.Items) != 1 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	item := usage.Items[0]
	if item.Requests != 3 || item.CachedRequests != 1 || item.CacheSavedMillicents != 50 || item.PromptCacheSavedMillicents != 23 ||
		item.CostMillicents != 70 || item.InputTokens != 40 {
		t.Fatalf("usage item = %+v", item)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_usage_daily SET cached_requests = requests + 1 WHERE integration_id = $1`, f.offer.ID); err == nil {
		t.Fatal("more cached requests than requests were stored")
	}
}
