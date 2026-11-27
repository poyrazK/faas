package aiprices

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Cost-control review 2026-11-19 (N17): the gateway reserves a generation
// model's longest response; dated names resolve to their family's.
func TestMaxOutputTokens(t *testing.T) {
	c := Default()
	family, ok := c.MaxOutputTokens(api.OutboundAIProviderOpenAI, "gpt-4o")
	if !ok || family < 4096 {
		t.Fatalf("gpt-4o = %d, %v", family, ok)
	}
	if dated, ok := c.MaxOutputTokens(api.OutboundAIProviderOpenAI, "gpt-4o-2099-01-01"); !ok || dated != family {
		t.Fatalf("dated gpt-4o = %d, %v; want the family's %d", dated, ok, family)
	}
	if _, ok := c.MaxOutputTokens(api.OutboundAIProviderOpenAI, "not-a-model"); ok {
		t.Fatal("an unknown model has a longest response")
	}
	all := defaultMaxOutputs()
	for _, provider := range []string{api.OutboundAIProviderOpenAI, api.OutboundAIProviderAnthropic} {
		if len(all[provider]) == 0 {
			t.Fatalf("%s has no longest responses", provider)
		}
	}
	for provider, models := range all {
		for model, limit := range models {
			if limit <= 0 || limit > 1_000_000 {
				t.Fatalf("%s/%s longest response = %d", provider, model, limit)
			}
		}
	}
}
