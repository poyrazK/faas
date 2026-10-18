package outboundai

// adr: 1435 — the content policy stores a response section; response
// moderation keeps the check's settings while requests are not moderated,
// and needs a complete check.

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestContentPolicyResponseSection(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	ctx := context.Background()
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, integrationID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	judge := aiIntegration(t, store, acct.ID, api.OutboundAIProviderAnthropic)
	put := func(body string) int {
		return serve(service.PutContentPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai-content-policy", integrationID, body).Code
	}
	check := `"moderation":{"action":"off","kind":"classifier","integration_id":"` + judge + `","model":"claude-haiku","categories":["prompt_injection","hate"]}`
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"response":{"email":"redact","secret":"block","blocked_terms":"block"},"blocked_terms":["falcon"]}`, http.StatusNoContent},
		{`{"response":{"blocked_terms":"redact"}}`, http.StatusBadRequest},
		{`{"response":{"moderation":"block"}}`, http.StatusBadRequest},
		{`{"moderation":{"action":"off","kind":"classifier","integration_id":"` + judge + `","model":"m","categories":["prompt_injection"]},"response":{"moderation":"flag"}}`, http.StatusBadRequest},
		{`{` + check + `,"response":{"moderation":"flag"}}`, http.StatusNoContent},
	} {
		if got := put(tc.body); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.body, got, tc.want)
		}
	}
	offers, err := store.ListOutboundIntegrationOffers(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, offer := range offers {
		if offer.ID != integrationID {
			continue
		}
		policy := offer.AIContentPolicy
		if policy.Response.Moderation != api.OutboundAIModerationFlag || policy.Moderation.Action != api.OutboundAIModerationOff ||
			policy.Moderation.IntegrationID != judge || policy.Response.Email != api.OutboundAIContentOff {
			t.Fatalf("stored policy = %+v", policy)
		}
	}
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, integrationID, state.OutboundAIPolicy{}); err != nil {
		t.Fatal(err)
	}
	offers, _ = store.ListOutboundIntegrationOffers(ctx, acct.ID)
	for _, offer := range offers {
		if offer.ID == integrationID && offer.AIContentPolicy.Response.Active() {
			t.Fatalf("turning AI off kept the response checks: %+v", offer.AIContentPolicy.Response)
		}
	}
}
