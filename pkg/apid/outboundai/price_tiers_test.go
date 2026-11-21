package outboundai

// adr: 20261027132936 — the catalog shows the long-context tier and the
// one-hour cache-write price billing charges, and a customer price list
// stores its own (amendment 3).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPriceCatalogShowsTiers(t *testing.T) {
	service, _, acct, _ := newTestService(t)
	lookup := func(provider, model string) api.OutboundAIPriceEntry {
		t.Helper()
		w := serve(service.GetPriceCatalog, acct, http.MethodGet, "/v1/outbound/ai-price-catalog?provider="+provider+"&model="+model, "", "")
		var out api.OutboundAIPriceCatalogResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Entries) != 1 {
			t.Fatalf("%s %s = %d %s", provider, model, w.Code, w.Body.String())
		}
		return out.Entries[0]
	}
	sonnet := lookup(api.OutboundAIProviderAnthropic, "claude-sonnet-4-5")
	if tier := sonnet.LongContext; tier == nil || *tier != (api.OutboundAILongContextTier{AboveInputTokens: 200_000, InputPercent: 200, OutputPercent: 150}) {
		t.Fatalf("sonnet tier = %+v", tier)
	}
	// Twice the 300,000 input price beats the 375,000 cache-write price.
	if sonnet.CacheWrite1hMillicentsPerMTok == nil || *sonnet.CacheWrite1hMillicentsPerMTok != 600_000 {
		t.Fatalf("sonnet 1h write = %v", sonnet.CacheWrite1hMillicentsPerMTok)
	}
	gemini := lookup(api.OutboundAIProviderGemini, "gemini-2.5-pro")
	if gemini.LongContext == nil || gemini.LongContext.AboveInputTokens != 200_000 || gemini.CacheWrite1hMillicentsPerMTok != nil {
		t.Fatalf("gemini = %+v %v", gemini.LongContext, gemini.CacheWrite1hMillicentsPerMTok)
	}
	if mini := lookup(api.OutboundAIProviderOpenAI, "gpt-4o-mini"); mini.LongContext != nil || mini.CacheWrite1hMillicentsPerMTok != nil {
		t.Fatalf("gpt-4o-mini = %+v %v", mini.LongContext, mini.CacheWrite1hMillicentsPerMTok)
	}
}

func TestPutPricesStoresTiers(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	path := "/v1/outbound/integrations/x/ai-prices"
	body := `{"entries":[{"model_prefix":"m","input_millicents_per_mtok":100,"output_millicents_per_mtok":200,` +
		`"cache_write_1h_millicents_per_mtok":300,"long_context":{"above_input_tokens":128000,"input_percent":200,"output_percent":200}}]}`
	if w := serve(service.PutPrices, acct, http.MethodPut, path, integrationID, body); w.Code != http.StatusNoContent {
		t.Fatalf("put prices = %d %s", w.Code, w.Body.String())
	}
	list, err := store.GetOutboundAIPrices(context.Background(), acct.ID, integrationID)
	if err != nil || len(list.Entries) != 1 || list.Entries[0].LongContext == nil || list.Entries[0].LongContext.AboveInputTokens != 128_000 ||
		list.Entries[0].CacheWrite1hMillicentsPerMTok == nil || *list.Entries[0].CacheWrite1hMillicentsPerMTok != 300 {
		t.Fatalf("stored = %+v, %v", list, err)
	}
	for _, tier := range []string{`{"above_input_tokens":999,"input_percent":200,"output_percent":200}`,
		`{"above_input_tokens":128000,"input_percent":99,"output_percent":200}`,
		`{"above_input_tokens":128000,"input_percent":200,"output_percent":1001}`} {
		bad := `{"entries":[{"model_prefix":"m","input_millicents_per_mtok":100,"output_millicents_per_mtok":200,"long_context":` + tier + `}]}`
		if w := serve(service.PutPrices, acct, http.MethodPut, path, integrationID, bad); w.Code != http.StatusBadRequest {
			t.Errorf("tier %s = %d", tier, w.Code)
		}
	}
}
