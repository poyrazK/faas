package outboundai

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/outbound/aiprices"
	"github.com/onebox-faas/faas/pkg/state"
)

// GetPriceCatalog returns one provider's built-in price catalog, or with
// ?model= the catalog entry that would price that model (ADR-994).
func (s *Service) GetPriceCatalog(w http.ResponseWriter, r *http.Request, _ state.Account) {
	query := r.URL.Query()
	provider, model := query.Get("provider"), query.Get("model")
	if provider == "" || !api.ValidOutboundAIProvider(provider) || len(model) > api.OutboundAIModelNameMaxBytes {
		problem(w, http.StatusBadRequest, api.CodeValidation,
			"provider must be openai, anthropic, gemini, azure_openai, bedrock or vertex, and model at most 128 bytes")
		return
	}
	catalog := aiprices.Default()
	entries := catalog.Models(provider)
	if model != "" {
		entries = nil
		if entry, ok := catalog.Lookup(provider, model); ok {
			entries = []aiprices.Entry{entry}
		}
	}
	out := api.OutboundAIPriceCatalogResponse{AIProvider: provider, Source: catalog.Source, Generated: catalog.Generated,
		Currency: catalog.Currency,
		// Released SDKs still require the deprecated fields (ADR 20261019123822).
		USDPerEUR: "1", FXSource: api.OutboundAIPriceCatalogFXSource, //nolint:staticcheck // deprecated fields stay until a release removes them
		Entries: make([]api.OutboundAIPriceEntry, 0, len(entries))}
	for _, entry := range entries {
		cached, write := entry.Price.CachedInput, entry.Price.CacheWrite
		out.Entries = append(out.Entries, api.OutboundAIPriceEntry{ModelPrefix: entry.Model,
			InputMillicentsPerMTok: entry.Price.Input, CachedInputMillicentsPerMTok: &cached,
			CacheWriteMillicentsPerMTok: &write, OutputMillicentsPerMTok: entry.Price.Output,
			// ADR-997 media prices; absent ones stay absent.
			InputImageMillicentsPerMTok: copyInt64(entry.Price.InputImage), OutputImageMillicentsPerMTok: copyInt64(entry.Price.OutputImage),
			MillicentsPerImage: copyInt64(entry.Price.Image), MillicentsPerAudioMinute: copyInt64(entry.Price.AudioMinute),
			MillicentsPerMillionCharacters: copyInt64(entry.Price.Characters),
			MillicentsPerVideoSecond:       copyInt64(entry.Price.VideoSecond),
			// ADR-1433 built-in tool prices.
			MillicentsPerWebSearch: copyInt64(entry.Price.WebSearch), MillicentsPerFileSearch: copyInt64(entry.Price.FileSearch),
			MillicentsPerCodeSessionGB: copyInt64(entry.Price.CodeSessionGB),
			// ADR 20261019072722 audio token prices.
			InputAudioMillicentsPerMTok:       copyInt64(entry.Price.InputAudio),
			CachedInputAudioMillicentsPerMTok: copyInt64(entry.Price.CachedInputAudio),
			CacheWrite1hMillicentsPerMTok:     catalogCacheWrite1h(entry),
			LongContext:                       catalogLongContext(entry.Model),
			OutputAudioMillicentsPerMTok:      copyInt64(entry.Price.OutputAudio)})
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// catalogCacheWrite1h is the one-hour cache-write price billing charges a
// catalog model, for the models whose provider bills one (ADR 20261027132936
// amendment 3).
func catalogCacheWrite1h(entry aiprices.Entry) *int64 {
	if !aiprices.HasCacheWrite1h(entry.Model) {
		return nil
	}
	price := aiprices.CacheWrite1hFallback(entry.Price.CacheWrite, entry.Price.Input)
	return &price
}

// catalogLongContext is the long-context tier billing applies to a catalog
// model, if it has one.
func catalogLongContext(model string) *api.OutboundAILongContextTier {
	tier, ok := aiprices.LongContextTierFor(model)
	if !ok {
		return nil
	}
	return &api.OutboundAILongContextTier{AboveInputTokens: tier.AboveInputTokens, InputPercent: tier.InputPercent,
		OutputPercent: tier.OutputPercent}
}
