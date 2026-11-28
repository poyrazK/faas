package aiprices

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt 2026-11-28: a call continuing stored context reserves its
// model's context window; dated names resolve to their family's, and the
// newest Claude models are priced.
func TestMaxInputTokens(t *testing.T) {
	c := Default()
	family, ok := c.MaxInputTokens(api.OutboundAIProviderOpenAI, "gpt-4o")
	if !ok || family < 100_000 {
		t.Fatalf("gpt-4o = %d, %v", family, ok)
	}
	if dated, ok := c.MaxInputTokens(api.OutboundAIProviderOpenAI, "gpt-4o-2099-01-01"); !ok || dated != family {
		t.Fatalf("dated gpt-4o = %d, %v; want the family's %d", dated, ok, family)
	}
	if _, ok := c.MaxInputTokens(api.OutboundAIProviderOpenAI, "not-a-model"); ok {
		t.Fatal("an unknown model has a context window")
	}
	for provider, models := range defaultMaxInputs() {
		for model, limit := range models {
			if limit <= 0 || limit > 10_000_000 {
				t.Fatalf("%s/%s context window = %d", provider, model, limit)
			}
		}
	}
	for _, model := range []string{"claude-fable-5-2", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"} {
		if _, ok := c.Lookup(api.OutboundAIProviderAnthropic, model); !ok {
			t.Errorf("%s has no price", model)
		}
	}
}
