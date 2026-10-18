package outbound

// adr: 1437 — the AI policy stores automatic prompt caching, the resolver
// reads it, and the database refuses it without an AI provider.

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAutoPromptCachePolicy(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{AutoPromptCache: true})
	ctx := context.Background()
	integration, err := f.integration(f.offer.ID)
	if err != nil || !integration.AIAutoPromptCache {
		t.Fatalf("resolved prompt cache = %v, %v", integration.AIAutoPromptCache, err)
	}
	offers, err := f.store.ListOutboundIntegrationOffers(ctx, f.account.ID)
	if err != nil || len(offers) != 1 || !offers[0].AIAutoPromptCache {
		t.Fatalf("listed prompt cache = %+v, %v", offers, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_integrations SET ai_provider = NULL WHERE id = $1`, f.offer.ID); err == nil {
		t.Fatal("automatic prompt caching was kept without an AI provider")
	}
	if err := f.store.SetOutboundAIPolicy(ctx, f.account.ID, f.offer.ID, state.OutboundAIPolicy{}); err != nil {
		t.Fatal(err)
	}
	if integration, err := f.integration(f.offer.ID); err != nil || integration.AIAutoPromptCache {
		t.Fatalf("turning AI off kept prompt caching: %v, %v", integration.AIAutoPromptCache, err)
	}
}
