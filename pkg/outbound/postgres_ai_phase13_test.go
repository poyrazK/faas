package outbound

// adr: 1435 — the content policy's response section is stored, read by the
// resolver with the moderation settings it uses while requests are not
// moderated, checked by the database, and cleared with AI handling.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresResponsePolicy(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	judge := f.secondAIIntegration(api.OutboundAIProviderOpenAI)
	policy := api.OutboundAIContentPolicy{
		BlockedTerms: []string{"falcon"},
		Moderation: api.OutboundAIModeration{Kind: api.OutboundAIModerationKindClassifier, IntegrationID: judge.ID, Model: "gpt-5-nano",
			Categories: []string{api.OutboundAIModerationHate}},
		Response: api.OutboundAIResponsePolicy{Email: api.OutboundAIContentRedact, Secret: api.OutboundAIContentBlock,
			BlockedTerms: api.OutboundAIContentBlock, Moderation: api.OutboundAIModerationFlag},
	}
	if err := f.store.SetOutboundAIContentPolicy(ctx, f.account.ID, f.offer.ID, policy); err != nil {
		t.Fatal(err)
	}
	integration, err := f.integration(f.offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := integration.AIContentPolicy
	if got.Response != policy.Response.Normalized() || got.Moderation.Action != api.OutboundAIModerationOff ||
		got.Moderation.IntegrationID != judge.ID || got.Moderation.Model != "gpt-5-nano" {
		t.Fatalf("resolved policy = %+v", got)
	}
	offers, err := f.store.ListOutboundIntegrationOffers(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, offer := range offers {
		if offer.ID == f.offer.ID && offer.AIContentPolicy.Response != policy.Response.Normalized() {
			t.Fatalf("listed response policy = %+v", offer.AIContentPolicy.Response)
		}
	}

	// Response moderation through another account's integration is refused.
	other := policy
	other.Moderation.IntegrationID = "00000000-0000-4000-8000-000000000001"
	if err := f.store.SetOutboundAIContentPolicy(ctx, f.account.ID, f.offer.ID, other); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("an unknown moderation integration = %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_integrations SET ai_moderation_kind = NULL WHERE id = $1`, f.offer.ID); err == nil {
		t.Fatal("response moderation without a check was stored")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_integrations SET ai_response_blocked_terms = 'redact' WHERE id = $1`, f.offer.ID); err == nil {
		t.Fatal("redacting blocked terms in responses was stored")
	}

	if err := f.store.SetOutboundAIPolicy(ctx, f.account.ID, f.offer.ID, state.OutboundAIPolicy{}); err != nil {
		t.Fatal(err)
	}
	if integration, err := f.integration(f.offer.ID); err != nil || integration.AIContentPolicy.Response.Active() {
		t.Fatalf("turning AI off kept the response checks: %+v, %v", integration.AIContentPolicy.Response, err)
	}
}
