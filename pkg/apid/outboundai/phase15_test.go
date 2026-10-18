package outboundai

// adr: 1437 — the AI policy stores automatic prompt caching, which needs an
// AI provider.

import (
	"context"
	"net/http"
	"testing"
)

func TestPutPolicyStoresAutoPromptCache(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	path := "/v1/outbound/integrations/x/ai-policy"
	if w := serve(service.PutPolicy, acct, http.MethodPut, path, integrationID, `{"ai_provider":"","auto_prompt_cache":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("prompt caching without a provider = %d", w.Code)
	}
	if w := serve(service.PutPolicy, acct, http.MethodPut, path, integrationID, `{"ai_provider":"anthropic","auto_prompt_cache":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("prompt caching = %d %s", w.Code, w.Body.String())
	}
	offers, err := store.ListOutboundIntegrationOffers(context.Background(), acct.ID)
	if err != nil || len(offers) != 1 || !offers[0].AIAutoPromptCache {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
}
