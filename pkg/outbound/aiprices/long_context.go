package aiprices

import "strings"

// LongContextTier is a model's higher price for a long prompt: when a call's
// prompt (all input tokens, cached and cache writes included) exceeds
// AboveInputTokens, input-side prices are multiplied by InputPercent/100 and
// output prices by OutputPercent/100. The catalog's list prices are the
// short-prompt prices, so without the tier a long call under-counts against
// cost caps (AI gateway review round 3, finding 2).
type LongContextTier struct {
	AboveInputTokens int64
	InputPercent     int64
	OutputPercent    int64
}

// longContextTiers are the published long-context tiers, matched as a
// substring of the catalog model so Bedrock and Vertex names match too.
// Anthropic Sonnet 4.x and Gemini 2.5 Pro double input and add half to
// output above 200k prompt tokens; Gemini 1.5 Pro doubles both above 128k.
var longContextTiers = []struct {
	family string
	tier   LongContextTier
}{
	{"claude-sonnet-4", LongContextTier{AboveInputTokens: 200_000, InputPercent: 200, OutputPercent: 150}},
	{"gemini-2.5-pro", LongContextTier{AboveInputTokens: 200_000, InputPercent: 200, OutputPercent: 150}},
	{"gemini-1.5-pro", LongContextTier{AboveInputTokens: 128_000, InputPercent: 200, OutputPercent: 200}},
}

// LongContextTierFor returns the long-context tier of a catalog model, if it
// has one. Speech models of a family are priced per character, not per
// prompt token, and have none.
func LongContextTierFor(model string) (LongContextTier, bool) {
	m := strings.ToLower(model)
	if strings.Contains(m, "tts") {
		return LongContextTier{}, false
	}
	for _, t := range longContextTiers {
		if strings.Contains(m, t.family) {
			return t.tier, true
		}
	}
	return LongContextTier{}, false
}

// CacheWrite1hFallback is the price of a one-hour cache write without a
// price of its own: one-hour writes cost twice the input price (five-minute
// ones 1.25 times, the cache-write price), so the higher of the two never
// under-counts them.
func CacheWrite1hFallback(cacheWrite, input int64) int64 {
	return max(cacheWrite, 2*input)
}

// HasCacheWrite1h reports whether a catalog model's provider bills cache
// writes kept for one hour: Claude, wherever it is served.
func HasCacheWrite1h(model string) bool {
	return strings.Contains(strings.ToLower(model), "claude")
}
