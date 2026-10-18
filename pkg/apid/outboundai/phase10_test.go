package outboundai

// adr: 1432 — the AI policy stores allow_unmetered, which needs a provider,
// and price lists take a price per second of video.

import (
	"context"
	"net/http"
	"testing"
)

func TestPutPolicyStoresAllowUnmetered(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	if w := serve(service.PutPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai-policy", integrationID,
		`{"ai_provider":"","allow_unmetered":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("allow_unmetered without a provider = %d", w.Code)
	}
	if w := serve(service.PutPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai-policy", integrationID,
		`{"ai_provider":"openai","allow_unmetered":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("allow_unmetered = %d %s", w.Code, w.Body.String())
	}
	offers, err := store.ListOutboundIntegrationOffers(context.Background(), acct.ID)
	if err != nil || len(offers) != 1 || !offers[0].AIAllowUnmetered {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	for body, want := range map[string]int{
		`{"entries":[{"model_prefix":"veo-3.1-generate-001@4k","input_millicents_per_mtok":0,"output_millicents_per_mtok":0,"millicents_per_video_second":53543}]}`: http.StatusNoContent,
		`{"entries":[{"model_prefix":"veo-3.1-generate-001","input_millicents_per_mtok":0,"output_millicents_per_mtok":0,"millicents_per_video_second":10000001}]}`: http.StatusBadRequest,
	} {
		if w := serve(service.PutPrices, acct, http.MethodPut, "/v1/outbound/integrations/x/ai-prices", integrationID, body); w.Code != want {
			t.Errorf("%s = %d %s", body, w.Code, w.Body.String())
		}
	}
}
