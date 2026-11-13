package api

import "testing"

func TestOutboundAISemanticCacheValid(t *testing.T) {
	good := OutboundAISemanticCache{Enabled: true, EmbeddingIntegrationID: "e", EmbeddingModel: "text-embedding-3-small"}
	for _, tc := range []struct {
		name     string
		cache    *OutboundAISemanticCache
		provider string
		want     bool
	}{
		{"nil", nil, "", true},
		{"disabled without fields", &OutboundAISemanticCache{}, "", true},
		{"enabled", &good, OutboundAIProviderOpenAI, true},
		{"no provider", &good, "", false},
		{"threshold low", &OutboundAISemanticCache{Enabled: true, ThresholdBasisPoints: 8_499, EmbeddingIntegrationID: "e", EmbeddingModel: "m"}, "openai", false},
		{"threshold high", &OutboundAISemanticCache{Enabled: true, ThresholdBasisPoints: 9_901, EmbeddingIntegrationID: "e", EmbeddingModel: "m"}, "openai", false},
		{"threshold max", &OutboundAISemanticCache{Enabled: true, ThresholdBasisPoints: 9_900, EmbeddingIntegrationID: "e", EmbeddingModel: "m"}, "openai", true},
		{"no embedding integration", &OutboundAISemanticCache{Enabled: true, EmbeddingModel: "m"}, "openai", false},
		{"bad model", &OutboundAISemanticCache{Enabled: true, EmbeddingIntegrationID: "e", EmbeddingModel: "-m"}, "openai", false},
	} {
		if got := tc.cache.Valid(tc.provider); got != tc.want {
			t.Errorf("%s: Valid = %v, want %v", tc.name, got, tc.want)
		}
	}
	if good.Threshold() != OutboundAISemanticCacheThresholdDefaultBP {
		t.Fatalf("default threshold = %d", good.Threshold())
	}
}
