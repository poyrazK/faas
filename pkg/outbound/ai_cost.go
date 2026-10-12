package outbound

import (
	"math/big"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/aiprices"
)

// AIPrice is one resolved price-list entry in millicents per million tokens
// (ADR-990). Resolution already replaced absent cached-input and
// cache-write prices with the input price.
type AIPrice struct {
	ModelPrefix string
	Input       int64
	CachedInput int64
	CacheWrite  int64
	Output      int64
}

// ResolveAIPrices converts an API price list into settlement prices.
func ResolveAIPrices(entries []api.OutboundAIPriceEntry) []AIPrice {
	prices := make([]AIPrice, 0, len(entries))
	for _, entry := range entries {
		price := AIPrice{
			ModelPrefix: entry.ModelPrefix, Input: entry.InputMillicentsPerMTok,
			CachedInput: entry.InputMillicentsPerMTok, CacheWrite: entry.InputMillicentsPerMTok,
			Output: entry.OutputMillicentsPerMTok,
		}
		if entry.CachedInputMillicentsPerMTok != nil {
			price.CachedInput = *entry.CachedInputMillicentsPerMTok
		}
		if entry.CacheWriteMillicentsPerMTok != nil {
			price.CacheWrite = *entry.CacheWriteMillicentsPerMTok
		}
		prices = append(prices, price)
	}
	return prices
}

// matchAIPrice picks the longest prefix that matches model; "*" matches
// any model and loses to every real prefix.
func matchAIPrice(prices []AIPrice, model string) (AIPrice, bool) {
	best, bestLen, found := AIPrice{}, -1, false
	for _, price := range prices {
		length := len(price.ModelPrefix)
		if price.ModelPrefix == "*" {
			length = 0
		} else if !strings.HasPrefix(model, price.ModelPrefix) {
			continue
		}
		if length > bestLen {
			best, bestLen, found = price, length, true
		}
	}
	return best, found
}

// aiPriceFor resolves one model's price (ADR-994): the integration's own
// list first, where "*" catches every model, then the built-in catalog for
// the integration's provider.
func aiPriceFor(integration Integration, model string) (AIPrice, bool) {
	if price, ok := matchAIPrice(integration.AIPrices, model); ok {
		return price, true
	}
	entry, ok := aiprices.Default().Lookup(integration.AIProvider, model)
	if !ok {
		return AIPrice{}, false
	}
	return AIPrice{ModelPrefix: entry.Model, Input: entry.Price.Input, CachedInput: entry.Price.CachedInput,
		CacheWrite: entry.Price.CacheWrite, Output: entry.Price.Output}, true
}

// aiCallCostMillicents prices one call of an integration.
func aiCallCostMillicents(integration Integration, usage AIUsage) int64 {
	price, ok := aiPriceFor(integration, usage.Model)
	if !ok {
		return 0
	}
	return priceCostMillicents(price, usage)
}

// aiCostMillicents prices one call from a price list alone.
func aiCostMillicents(prices []AIPrice, usage AIUsage) int64 {
	price, ok := matchAIPrice(prices, usage.Model)
	if !ok {
		return 0
	}
	return priceCostMillicents(price, usage)
}

// priceCostMillicents prices one call, rounding up to a whole millicent so
// a budget never under-counts. Uncached input excludes cached reads and
// cache writes, which have their own prices. Big integers keep tokens times
// price from overflowing.
func priceCostMillicents(price AIPrice, usage AIUsage) int64 {
	if !usage.Known {
		return 0
	}
	uncached := usage.InputTokens - usage.CachedInputTokens - usage.CacheWriteTokens
	if uncached < 0 {
		uncached = 0
	}
	total := new(big.Int)
	for _, term := range [][2]int64{
		{uncached, price.Input}, {usage.CachedInputTokens, price.CachedInput},
		{usage.CacheWriteTokens, price.CacheWrite}, {usage.OutputTokens, price.Output},
	} {
		total.Add(total, new(big.Int).Mul(big.NewInt(term[0]), big.NewInt(term[1])))
	}
	million := big.NewInt(1_000_000)
	quotient, remainder := new(big.Int).QuoRem(total, million, new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() || quotient.Int64() > api.MaxOutboundAICostMillicents {
		return api.MaxOutboundAICostMillicents
	}
	return quotient.Int64()
}
