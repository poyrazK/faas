package outboundai

// adr: 20261112194947 — the AI policy stores the semantic cache for Pro and
// Scale plans, on an integration with a response cache, embedding through
// one of the account's OpenAI or Gemini integrations.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPutPolicyStoresSemanticCache(t *testing.T) {
	service, store, acct, _ := newTestService(t)
	ctx := context.Background()
	policy := api.DefaultOutboundRequestPolicy()
	policy.ResponseCacheTTLSeconds = 60
	cached, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: acct.ID, Name: "cached", Origin: "https://api.openai.com",
		AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer", RequestPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	embedder := uuid.NewString()
	store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{ID: embedder, AccountID: acct.ID, Name: "embedder",
		Origin: "https://api.openai.com", Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed",
		AIProvider: api.OutboundAIProviderOpenAI})
	claude := uuid.NewString()
	store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{ID: claude, AccountID: acct.ID, Name: "claude",
		Origin: "https://api.anthropic.com", Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed",
		AIProvider: api.OutboundAIProviderAnthropic})
	path := "/v1/outbound/integrations/x/ai-policy"
	body := func(embedding string, extra string) string {
		return `{"ai_provider":"openai","semantic_cache":{"enabled":true,"embedding_integration_id":"` + embedding +
			`","embedding_model":"text-embedding-3-small"` + extra + `}}`
	}
	hobby := acct
	hobby.Plan = api.PlanHobby
	for _, tc := range []struct {
		name        string
		acct        state.Account
		integration string
		body        string
		want        int
	}{
		{"hobby plan", hobby, cached.ID, body(embedder, ""), http.StatusPaymentRequired},
		{"threshold out of range", acct, cached.ID, body(embedder, `,"threshold_basis_points":8000`), http.StatusBadRequest},
		{"no response cache", acct, "", body(embedder, ""), http.StatusBadRequest},
		{"anthropic embedder", acct, cached.ID, body(claude, ""), http.StatusBadRequest},
		{"stored", acct, cached.ID, body(embedder, `,"share_across_users":true`), http.StatusNoContent},
	} {
		integration := tc.integration
		if integration == "" {
			offers, _ := store.ListOutboundIntegrationOffers(ctx, acct.ID)
			for _, offer := range offers {
				if offer.Name == "llm" {
					integration = offer.ID
				}
			}
		}
		w := serve(service.PutPolicy, tc.acct, http.MethodPut, path, integration, tc.body)
		if w.Code != tc.want {
			t.Fatalf("%s = %d %s", tc.name, w.Code, w.Body.String())
		}
		if tc.want == http.StatusBadRequest && !strings.Contains(w.Body.String(), "semantic_cache needs") {
			t.Fatalf("%s detail = %s", tc.name, w.Body.String())
		}
	}
	offers, err := store.ListOutboundIntegrationOffers(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, offer := range offers {
		if offer.ID != cached.ID {
			continue
		}
		semantic := offer.AISemanticCache
		if semantic == nil || semantic.Threshold() != api.OutboundAISemanticCacheThresholdDefaultBP || semantic.ThresholdBasisPoints != 9500 ||
			semantic.EmbeddingIntegrationID != embedder || !semantic.ShareAcrossUsers {
			t.Fatalf("stored semantic cache = %+v", semantic)
		}
		return
	}
	t.Fatal("the cached integration was not listed")
}
