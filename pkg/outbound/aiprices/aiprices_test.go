package aiprices

// adr: 994 — the embedded catalog parses, covers every gateway provider and
// resolves dated, versioned and region-prefixed model names to a family.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDefaultCatalogCoversEveryProvider(t *testing.T) {
	catalog := Default()
	for _, provider := range []string{api.OutboundAIProviderOpenAI, api.OutboundAIProviderAnthropic, api.OutboundAIProviderGemini,
		api.OutboundAIProviderAzureOpenAI, api.OutboundAIProviderBedrock, api.OutboundAIProviderVertex} {
		models := catalog.Models(provider)
		if len(models) < 5 {
			t.Errorf("%s has %d catalog models", provider, len(models))
		}
		for _, entry := range models {
			price := entry.Price
			media := price.OutputImage != nil || price.Image != nil || price.AudioMinute != nil || price.Characters != nil
			if price.Input == 0 && price.Output == 0 && !media {
				t.Errorf("%s %s is free in the catalog", provider, entry.Model)
			}
		}
	}
	if catalog.Source == "" || catalog.USDPerEUR == "" || catalog.Generated == "" {
		t.Fatalf("catalog header = %+v", catalog)
	}
}

func TestLookup(t *testing.T) {
	catalog, err := Parse([]byte(`{
		"source": "test", "generated": "2026-10-12", "usd_per_eur": "1", "fx_source": "test",
		"fields": ["input", "cached_input", "cache_write", "output", "input_image", "output_image", "image", "audio_minute", "characters"],
		"providers": {
			"openai": {"gpt-4o": [1, 1, 1, 4], "gpt-4o-mini": [2, 2, 2, 8], "o1": [3, 3, 3, 12]},
			"bedrock": {"anthropic.claude-3-5-sonnet-20241022-v2:0": [5, 5, 5, 20], "anthropic.claude-3-5-haiku": [6, 6, 6, 24]},
			"vertex": {"claude-sonnet-4-5": [7, 7, 7, 28]}
		}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		provider, model, want string
	}{
		{"openai", "gpt-4o", "gpt-4o"},
		{"openai", "gpt-4o-2024-08-06", "gpt-4o"},
		{"openai", "gpt-4o-mini-2024-07-18", "gpt-4o-mini"},
		{"openai", "gpt-4", ""},
		{"openai", "gpt-4o.1", ""},
		{"openai", "o1-preview", "o1"},
		{"bedrock", "us.anthropic.claude-3-5-sonnet-20241022-v2:0", "anthropic.claude-3-5-sonnet-20241022-v2:0"},
		{"bedrock", "arn:aws:bedrock:us-east-1:123456789012:inference-profile/eu.anthropic.claude-3-5-haiku-20241022-v1:0", "anthropic.claude-3-5-haiku"},
		{"vertex", "claude-sonnet-4-5@20250929", "claude-sonnet-4-5"},
		{"vertex", "models/claude-sonnet-4-5", "claude-sonnet-4-5"},
		{"anthropic", "claude-sonnet-4-5", ""},
		{"openai", "", ""},
	} {
		entry, ok := catalog.Lookup(tc.provider, tc.model)
		if got := entry.Model; ok != (tc.want != "") || got != tc.want {
			t.Errorf("Lookup(%q, %q) = %q, %v; want %q", tc.provider, tc.model, got, ok, tc.want)
		}
	}
}

func TestParseRejectsInvalidCatalogs(t *testing.T) {
	header := `"source": "s", "generated": "g", "usd_per_eur": "1", "fx_source": "f", "fields": ["input", "cached_input", "cache_write", "output", "input_image", "output_image", "image", "audio_minute", "characters"]`
	for name, body := range map[string]string{
		"unknown provider": `{` + header + `, "providers": {"mistral": {"m": [1, 1, 1, 1]}}}`,
		"negative price":   `{` + header + `, "providers": {"openai": {"m": [-1, 1, 1, 1]}}}`,
		"missing price":    `{` + header + `, "providers": {"openai": {"m": [1, 1, 1]}}}`,
		"too dear":         `{` + header + `, "providers": {"openai": {"m": [1, 1, 1, 1000000001]}}}`,
		"image too dear":   `{` + header + `, "providers": {"openai": {"m": [1, 1, 1, 1, null, null, 10000001]}}}`,
		"negative media":   `{` + header + `, "providers": {"openai": {"m": [1, 1, 1, 1, null, null, null, -1]}}}`,
		"field order":      `{"source": "s", "generated": "g", "usd_per_eur": "1", "fx_source": "f", "fields": ["output"], "providers": {}}`,
	} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, body)
		}
	}
}

// adr: 997 — the catalog carries media prices for image, transcription and
// speech models.
func TestCatalogMediaPrices(t *testing.T) {
	catalog := Default()
	for _, tc := range []struct {
		provider, model string
		check           func(Price) bool
	}{
		{"openai", "gpt-image-1", func(p Price) bool { return p.OutputImage != nil && *p.OutputImage > 0 && p.InputImage != nil }},
		{"openai", "whisper-1", func(p Price) bool { return p.AudioMinute != nil && *p.AudioMinute > 0 }},
		{"openai", "tts-1", func(p Price) bool { return p.Characters != nil && *p.Characters > 0 }},
		{"gemini", "imagen-3.0-generate-001", func(p Price) bool { return p.Image != nil && *p.Image > 0 }},
		{"gemini", "gemini-2.5-flash-image", func(p Price) bool { return p.OutputImage != nil && *p.OutputImage > p.Output }},
		{"openai", "gpt-4o", func(p Price) bool { return p.OutputImage == nil && p.Image == nil && p.AudioMinute == nil }},
	} {
		entry, ok := catalog.Lookup(tc.provider, tc.model)
		if !ok || !tc.check(entry.Price) {
			t.Errorf("%s %s = %+v, %v", tc.provider, tc.model, entry.Price, ok)
		}
	}
}
