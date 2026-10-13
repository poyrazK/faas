package outbound_test

// adr: 997 — Postgres stores an integration's content policy, outboundd
// resolves it, and turning AI handling off clears it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAIContentPolicy(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-ai-policy-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "ai-policy",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodPost},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := api.OutboundAIContentPolicy{Email: api.OutboundAIContentRedact, Secret: api.OutboundAIContentBlock, BlockedTerms: []string{"Project Falcon"}}
	if err := store.SetOutboundAIContentPolicy(ctx, account.ID, offer.ID, policy); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("a policy without an AI provider = %v", err)
	}
	if err := store.SetOutboundAIContentPolicy(ctx, account.ID, uuid.NewString(), policy); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("an unknown integration = %v", err)
	}
	if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundAIContentPolicy(ctx, account.ID, offer.ID, policy); err != nil {
		t.Fatal(err)
	}
	offers, err := store.ListOutboundIntegrationOffers(ctx, account.ID)
	if err != nil || len(offers) != 1 || offers[0].AIContentPolicy.Email != api.OutboundAIContentRedact ||
		offers[0].AIContentPolicy.PaymentCard != api.OutboundAIContentOff || len(offers[0].AIContentPolicy.BlockedTerms) != 1 {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Integration(ctx, offer.ID)
	if err != nil || resolved.AIContentPolicy.Secret != api.OutboundAIContentBlock || resolved.AIContentPolicy.BlockedTerms[0] != "Project Falcon" {
		t.Fatalf("resolved policy = %+v, %v", resolved.AIContentPolicy, err)
	}
	// Turning AI handling off clears the policy with it.
	if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, state.OutboundAIPolicy{}); err != nil {
		t.Fatal(err)
	}
	offers, err = store.ListOutboundIntegrationOffers(ctx, account.ID)
	if err != nil || offers[0].AIContentPolicy.Active() {
		t.Fatalf("policy after AI was turned off = %+v, %v", offers[0].AIContentPolicy, err)
	}
}
