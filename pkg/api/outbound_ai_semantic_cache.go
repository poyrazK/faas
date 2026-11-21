package api

import "github.com/onebox-faas/faas/pkg/lazyregexp"

// OutboundAISemanticCache answers a call from the response cache when its
// last user turn means the same as a cached call's and everything else
// matches exactly (ADR 20261112194947). It needs the response cache and a
// plan that includes it.
type OutboundAISemanticCache struct {
	Enabled bool `json:"enabled"`
	// ThresholdBasisPoints is the cosine similarity a hit needs, in basis
	// points (8500 to 9900); zero means the default, 9500.
	ThresholdBasisPoints int `json:"threshold_basis_points,omitempty"`
	// EmbeddingIntegrationID names one of the account's enabled OpenAI or
	// Gemini integrations, which embeds each candidate call's text on its
	// own key; EmbeddingModel is the model it uses.
	EmbeddingIntegrationID string `json:"embedding_integration_id"`
	EmbeddingModel         string `json:"embedding_model"`
	// ShareAcrossUsers lets calls of different end users
	// (X-Gregale-AI-User) share answers. Off, each end user has their own.
	ShareAcrossUsers bool `json:"share_across_users,omitempty"`
}

// OutboundAISemanticCacheEmbeddingProviders are the providers whose
// embedding endpoints the semantic cache calls.
var OutboundAISemanticCacheEmbeddingProviders = []string{OutboundAIProviderOpenAI, OutboundAIProviderGemini}

var outboundAIEmbeddingModelPattern = lazyregexp.New(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}$`)

// Threshold is the similarity a hit needs, in basis points.
func (c OutboundAISemanticCache) Threshold() int {
	if c.ThresholdBasisPoints == 0 {
		return OutboundAISemanticCacheThresholdDefaultBP
	}
	return c.ThresholdBasisPoints
}

// Valid reports whether the settings can be stored for an integration with
// the given AI provider. Disabled settings are always valid.
func (c *OutboundAISemanticCache) Valid(provider string) bool {
	if c == nil || !c.Enabled {
		return true
	}
	threshold := c.Threshold()
	return provider != "" && threshold >= OutboundAISemanticCacheThresholdMinBP && threshold <= OutboundAISemanticCacheThresholdMaxBP &&
		c.EmbeddingIntegrationID != "" && outboundAIEmbeddingModelPattern.MatchString(c.EmbeddingModel)
}

// OutboundAISemanticCacheInvalidDetail explains rejected settings.
const OutboundAISemanticCacheInvalidDetail = "semantic_cache needs an AI provider, the response cache (response_cache_ttl_seconds above 0), " +
	"a threshold_basis_points of 8500 to 9900, an embedding_integration_id naming one of the account's enabled OpenAI or Gemini " +
	"integrations, and an embedding_model of 1 to 128 letters, digits or . _ : / @ -"
