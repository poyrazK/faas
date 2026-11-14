package outbound

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/aiprices"
)

// adr: 20261027132936 — a customer price list entry's long-context tier and
// one-hour cache-write price are billed like the catalog's (amendment 3).
func TestCustomPriceTiers(t *testing.T) {
	prices := ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "tiered", InputMillicentsPerMTok: 1_000_000, OutputMillicentsPerMTok: 1_000_000,
			CacheWriteMillicentsPerMTok: int64Ref(1_250_000), CacheWrite1hMillicentsPerMTok: int64Ref(3_000_000),
			LongContext: &api.OutboundAILongContextTier{AboveInputTokens: 1_000, InputPercent: 200, OutputPercent: 150}},
		{ModelPrefix: "flat", InputMillicentsPerMTok: 1_000_000, OutputMillicentsPerMTok: 1_000_000,
			CacheWriteMillicentsPerMTok: int64Ref(1_250_000)},
	})
	for _, tc := range []struct {
		name  string
		usage AIUsage
		want  int64
	}{
		// At or below the threshold, list prices.
		{"short prompt", AIUsage{Model: "tiered", Known: true, InputTokens: 1_000, OutputTokens: 1_000}, 1_000 + 1_000},
		// Above it, 2x input and 1.5x output.
		{"long prompt", AIUsage{Model: "tiered", Known: true, InputTokens: 2_000, OutputTokens: 1_000}, 4_000 + 1_500},
		{"no tier", AIUsage{Model: "flat", Known: true, InputTokens: 2_000, OutputTokens: 1_000}, 2_000 + 1_000},
		// One-hour writes at the entry's own price.
		{"own 1h price", AIUsage{Model: "tiered", Known: true, InputTokens: 1_000, CacheWriteTokens: 1_000, CacheWrite1hTokens: 1_000}, 3_000},
		// Without one, the higher of the cache-write price and twice input.
		{"fallback 1h price", AIUsage{Model: "flat", Known: true, InputTokens: 1_000, CacheWriteTokens: 1_000, CacheWrite1hTokens: 1_000}, 2_000},
		{"5m writes unchanged", AIUsage{Model: "tiered", Known: true, InputTokens: 1_000, CacheWriteTokens: 1_000}, 1_250},
	} {
		if got := aiCostMillicents(prices, tc.usage); got != tc.want {
			t.Errorf("%s: cost = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCacheWrite1hFallback(t *testing.T) {
	if got := aiprices.CacheWrite1hFallback(375_000, 300_000); got != 600_000 {
		t.Fatalf("fallback = %d", got)
	}
	if got := aiprices.CacheWrite1hFallback(900_000, 300_000); got != 900_000 {
		t.Fatalf("fallback keeps a higher cache-write price: %d", got)
	}
	if !aiprices.HasCacheWrite1h("anthropic.claude-sonnet-4-5-v1:0") || aiprices.HasCacheWrite1h("gpt-4o") {
		t.Fatal("HasCacheWrite1h")
	}
}
