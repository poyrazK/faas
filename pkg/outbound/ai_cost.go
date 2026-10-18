package outbound

import (
	"math/big"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/aiprices"
)

// AIPrice is one resolved price-list entry (ADR-990). Token prices are
// millicents per million tokens; resolution already replaced absent
// cached-input, cache-write and input-image prices with the input price and
// an absent output-image price with the output price. The other media
// prices (ADR-997) are zero when absent: Image per image, AudioMinute per
// minute of input audio and Characters per million input characters, and
// VideoSecond per second of generated video (ADR-1432).
type AIPrice struct {
	ModelPrefix string
	Input       int64
	CachedInput int64
	CacheWrite  int64
	Output      int64
	InputImage  int64
	OutputImage int64
	Image       int64
	AudioMinute int64
	Characters  int64
	VideoSecond int64
	// Built-in tool prices (ADR-1433): per web search, per file search and
	// per GB container session; zero when absent.
	WebSearch     int64
	FileSearch    int64
	CodeSessionGB int64
}

// resolveToolPrices fills the built-in tool prices of a resolved price.
func resolveToolPrices(price *AIPrice, webSearch, fileSearch, codeSessionGB *int64) {
	for _, field := range []struct {
		dst *int64
		src *int64
	}{{&price.WebSearch, webSearch}, {&price.FileSearch, fileSearch}, {&price.CodeSessionGB, codeSessionGB}} {
		if field.src != nil {
			*field.dst = *field.src
		}
	}
}

// resolveMediaPrices fills the media prices of a resolved price.
func resolveMediaPrices(price *AIPrice, inputImage, outputImage, image, audioMinute, characters, videoSecond *int64) {
	price.InputImage, price.OutputImage = price.Input, price.Output
	if inputImage != nil {
		price.InputImage = *inputImage
	}
	if outputImage != nil {
		price.OutputImage = *outputImage
	}
	for _, field := range []struct {
		dst *int64
		src *int64
	}{{&price.Image, image}, {&price.AudioMinute, audioMinute}, {&price.Characters, characters}, {&price.VideoSecond, videoSecond}} {
		if field.src != nil {
			*field.dst = *field.src
		}
	}
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
		resolveMediaPrices(&price, entry.InputImageMillicentsPerMTok, entry.OutputImageMillicentsPerMTok,
			entry.MillicentsPerImage, entry.MillicentsPerAudioMinute, entry.MillicentsPerMillionCharacters, entry.MillicentsPerVideoSecond)
		resolveToolPrices(&price, entry.MillicentsPerWebSearch, entry.MillicentsPerFileSearch, entry.MillicentsPerCodeSessionGB)
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
	price := AIPrice{ModelPrefix: entry.Model, Input: entry.Price.Input, CachedInput: entry.Price.CachedInput,
		CacheWrite: entry.Price.CacheWrite, Output: entry.Price.Output}
	resolveMediaPrices(&price, entry.Price.InputImage, entry.Price.OutputImage, entry.Price.Image,
		entry.Price.AudioMinute, entry.Price.Characters, entry.Price.VideoSecond)
	resolveToolPrices(&price, entry.Price.WebSearch, entry.Price.FileSearch, entry.Price.CodeSessionGB)
	return price, true
}

// aiCallCostMillicents prices one call of an integration.
func aiCallCostMillicents(integration Integration, usage AIUsage) int64 {
	price, ok := aiPriceFor(integration, usage.priceModel())
	if !ok {
		return 0
	}
	return priceCostMillicents(price, usage)
}

// aiCostMillicents prices one call from a price list alone.
func aiCostMillicents(prices []AIPrice, usage AIUsage) int64 {
	price, ok := matchAIPrice(prices, usage.priceModel())
	if !ok {
		return 0
	}
	return priceCostMillicents(price, usage)
}

// priceCostMillicents prices one call, rounding up to a whole millicent so
// a budget never under-counts. Uncached input excludes cached reads, cache
// writes and input image tokens, and text output excludes output image
// tokens, which have their own prices. A call is priced per image only when
// it reports no output image tokens, so a model priced both ways is not
// charged twice (ADR-997). Big integers keep amounts times prices from
// overflowing.
func priceCostMillicents(price AIPrice, usage AIUsage) int64 {
	if !usage.Known {
		return 0
	}
	uncached := max(usage.InputTokens-usage.CachedInputTokens-usage.CacheWriteTokens-usage.InputImageTokens, 0)
	textOutput := max(usage.OutputTokens-usage.OutputImageTokens, 0)
	// Every term is scaled to sixty million: per-million prices by 60,
	// per-image, per-second and per-use prices by 60,000,000 and per-minute
	// prices by 1,000,000.
	total := new(big.Int)
	for _, term := range [][3]int64{
		{uncached, price.Input, 60}, {usage.CachedInputTokens, price.CachedInput, 60},
		{usage.CacheWriteTokens, price.CacheWrite, 60}, {usage.InputImageTokens, price.InputImage, 60},
		{textOutput, price.Output, 60}, {usage.OutputImageTokens, price.OutputImage, 60},
		{usage.Characters, price.Characters, 60}, {usage.AudioSeconds, price.AudioMinute, 1_000_000},
		{usage.VideoSeconds, price.VideoSecond, 60_000_000},
		{usage.WebSearches, price.WebSearch, 60_000_000}, {usage.FileSearches, price.FileSearch, 60_000_000},
		{usage.CodeSessionGB, price.CodeSessionGB, 60_000_000},
	} {
		total.Add(total, new(big.Int).Mul(big.NewInt(term[0]), big.NewInt(term[1]*term[2])))
	}
	if usage.OutputImageTokens == 0 {
		total.Add(total, new(big.Int).Mul(big.NewInt(usage.Images), big.NewInt(price.Image*60_000_000)))
	}
	scale := big.NewInt(60_000_000)
	quotient, remainder := new(big.Int).QuoRem(total, scale, new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() || quotient.Int64() > api.MaxOutboundAICostMillicents {
		return api.MaxOutboundAICostMillicents
	}
	return quotient.Int64()
}
