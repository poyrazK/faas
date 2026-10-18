package outbound

// adr: 1433 — tool prices round-trip through Postgres to the resolver, and
// settled tool uses roll up by day.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAIToolFees(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	webSearch, fileSearch, session := int64(893), int64(224), int64(2678)
	if err := f.store.SetOutboundAIPrices(ctx, f.account.ID, f.offer.ID, []api.OutboundAIPriceEntry{{ModelPrefix: "gpt-5",
		InputMillicentsPerMTok: 100_000, OutputMillicentsPerMTok: 800_000, MillicentsPerWebSearch: &webSearch,
		MillicentsPerFileSearch: &fileSearch, MillicentsPerCodeSessionGB: &session}}); err != nil {
		t.Fatal(err)
	}
	prices, err := f.store.GetOutboundAIPrices(ctx, f.account.ID, f.offer.ID)
	if err != nil || len(prices.Entries) != 1 || *prices.Entries[0].MillicentsPerWebSearch != webSearch ||
		*prices.Entries[0].MillicentsPerFileSearch != fileSearch || *prices.Entries[0].MillicentsPerCodeSessionGB != session {
		t.Fatalf("stored prices = %+v, %v", prices, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_price_lists SET effective_from = now() - interval '1 minute' WHERE integration_id = $1`,
		f.offer.ID); err != nil {
		t.Fatal(err)
	}
	integration, err := f.integration(f.offer.ID)
	if err != nil || len(integration.AIPrices) != 1 || integration.AIPrices[0].WebSearch != webSearch ||
		integration.AIPrices[0].FileSearch != fileSearch || integration.AIPrices[0].CodeSessionGB != session {
		t.Fatalf("resolved prices = %+v, %v", integration.AIPrices, err)
	}
	usage := AIUsage{Model: "gpt-5", Known: true, InputTokens: 10, OutputTokens: 5, WebSearches: 3, FileSearches: 2, CodeSessionGB: 4}
	cost := aiCallCostMillicents(integration, usage)
	if cost < 3*webSearch+2*fileSearch+4*session {
		t.Fatalf("cost %d leaves out tool fees", cost)
	}
	for range 2 {
		if _, err := backend.RecordAIUsageAlerts(ctx, AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID,
			Outcome: AIUsageOutcomeKnown, Usage: usage, CostMillicents: cost}); err != nil {
			t.Fatal(err)
		}
	}
	day, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range day.Items {
		if item.Model == "gpt-5" {
			if item.WebSearches != 6 || item.FileSearches != 4 || item.CodeSessionGB != 8 || item.CostMillicents != 2*cost {
				t.Fatalf("tool usage = %+v", item)
			}
			return
		}
	}
	t.Fatalf("no gpt-5 usage in %+v", day.Items)
}
