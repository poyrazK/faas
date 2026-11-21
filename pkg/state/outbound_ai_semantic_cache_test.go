package state_test

// adr: 20261112194947 — the AI policy stores the semantic cache only on an
// integration with a response cache, embedding through one of the
// account's enabled OpenAI or Gemini integrations, in Postgres and in
// memory, and lists it with the integration.

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type semanticCacheStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateOutboundIntegration(context.Context, state.OutboundIntegrationOffer) (state.OutboundIntegrationOffer, error)
	SetOutboundAIPolicy(context.Context, string, string, state.OutboundAIPolicy) error
	ListOutboundIntegrationOffers(context.Context, string) ([]state.OutboundIntegrationOffer, error)
}

func checkSemanticCachePolicy(t *testing.T, store semanticCacheStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "semantic-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string, ttl int) string {
		policy := api.OutboundRequestPolicy{RatePerSecond: 10, Burst: 10, MaxInFlight: 10, RequestTimeoutMS: 30_000, ResponseCacheTTLSeconds: ttl}
		offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
			ID: uuid.NewString(), AccountID: acct.ID, Name: name, Origin: "https://api.openai.com",
			AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
			CredentialSource: "customer_sealed", OwnerKind: "customer", RequestPolicy: policy,
		})
		if err != nil {
			t.Fatal(err)
		}
		return offer.ID
	}
	cached, uncached, embedder, claude := create("cached", 60), create("uncached", 0), create("embedder", 0), create("claude", 0)
	for id, provider := range map[string]string{embedder: api.OutboundAIProviderOpenAI, claude: api.OutboundAIProviderAnthropic} {
		if err := store.SetOutboundAIPolicy(ctx, acct.ID, id, state.OutboundAIPolicy{Provider: provider}); err != nil {
			t.Fatal(err)
		}
	}
	semantic := func(embedding string) state.OutboundAIPolicy {
		return state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI, SemanticCache: &api.OutboundAISemanticCache{
			Enabled: true, EmbeddingIntegrationID: embedding, EmbeddingModel: "text-embedding-3-small"}}
	}
	for name, tc := range map[string]struct {
		integration string
		policy      state.OutboundAIPolicy
	}{
		"no response cache":   {uncached, semantic(embedder)},
		"anthropic embedder":  {cached, semantic(claude)},
		"unknown embedder":    {cached, semantic(uuid.NewString())},
		"embedder not a uuid": {cached, semantic("embedder")},
		"provider off":        {cached, state.OutboundAIPolicy{SemanticCache: semantic(embedder).SemanticCache}},
	} {
		if err := store.SetOutboundAIPolicy(ctx, acct.ID, tc.integration, tc.policy); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("%s: err = %v, want invalid", name, err)
		}
	}
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, cached, semantic(embedder)); err != nil {
		t.Fatal(err)
	}
	offers, err := store.ListOutboundIntegrationOffers(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, offer := range offers {
		if offer.ID == cached {
			if got := offer.AISemanticCache; got == nil || got.ThresholdBasisPoints != 9500 || got.EmbeddingIntegrationID != embedder {
				t.Fatalf("listed semantic cache = %+v", got)
			}
		} else if offer.AISemanticCache != nil {
			t.Fatalf("%s lists a semantic cache", offer.Name)
		}
	}
	// Turning it off clears it.
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, cached, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	offers, _ = store.ListOutboundIntegrationOffers(ctx, acct.ID)
	for _, offer := range offers {
		if offer.AISemanticCache != nil {
			t.Fatalf("%s kept its semantic cache", offer.Name)
		}
	}
}

func TestMemOutboundAISemanticCache(t *testing.T) {
	checkSemanticCachePolicy(t, state.NewMemStore())
}

func TestPgOutboundAISemanticCache(t *testing.T) {
	s, _, _ := pgWithPool(t)
	checkSemanticCachePolicy(t, s)
}
