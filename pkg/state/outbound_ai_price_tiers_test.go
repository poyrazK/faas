package state_test

// adr: 20261027132936 — a price list stores each entry's one-hour
// cache-write price and long-context tier, in Postgres and in memory
// (amendment 3).

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type priceTiersStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateOutboundIntegration(context.Context, state.OutboundIntegrationOffer) (state.OutboundIntegrationOffer, error)
	SetOutboundAIPolicy(context.Context, string, string, state.OutboundAIPolicy) error
	SetOutboundAIPrices(context.Context, string, string, []api.OutboundAIPriceEntry) error
	GetOutboundAIPrices(context.Context, string, string) (state.OutboundAIPriceList, error)
}

func checkPriceTiers(t *testing.T, store priceTiersStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "tiers-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: acct.ID, Name: "llm", Origin: "https://api.anthropic.com",
		AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer",
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 10, Burst: 10, MaxInFlight: 10, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, offer.ID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderAnthropic}); err != nil {
		t.Fatal(err)
	}
	write1h := int64(600_000)
	tier := &api.OutboundAILongContextTier{AboveInputTokens: 200_000, InputPercent: 200, OutputPercent: 150}
	if err := store.SetOutboundAIPrices(ctx, acct.ID, offer.ID, []api.OutboundAIPriceEntry{
		{ModelPrefix: "claude", InputMillicentsPerMTok: 300_000, OutputMillicentsPerMTok: 1_500_000,
			CacheWrite1hMillicentsPerMTok: &write1h, LongContext: tier},
		{ModelPrefix: "flat", InputMillicentsPerMTok: 1, OutputMillicentsPerMTok: 2},
	}); err != nil {
		t.Fatal(err)
	}
	list, err := store.GetOutboundAIPrices(ctx, acct.ID, offer.ID)
	if err != nil || len(list.Entries) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	for _, entry := range list.Entries {
		switch entry.ModelPrefix {
		case "claude":
			if entry.CacheWrite1hMillicentsPerMTok == nil || *entry.CacheWrite1hMillicentsPerMTok != write1h ||
				entry.LongContext == nil || *entry.LongContext != *tier {
				t.Fatalf("claude = %+v %+v", entry, entry.LongContext)
			}
		case "flat":
			if entry.CacheWrite1hMillicentsPerMTok != nil || entry.LongContext != nil {
				t.Fatalf("flat = %+v", entry)
			}
		}
	}
	bad := []api.OutboundAIPriceEntry{{ModelPrefix: "m", InputMillicentsPerMTok: 1, OutputMillicentsPerMTok: 1,
		LongContext: &api.OutboundAILongContextTier{AboveInputTokens: 10, InputPercent: 200, OutputPercent: 200}}}
	if err := store.SetOutboundAIPrices(ctx, acct.ID, offer.ID, bad); err == nil {
		t.Fatal("an out-of-range tier was stored")
	}
}

func TestMemOutboundAIPriceTiers(t *testing.T) {
	checkPriceTiers(t, state.NewMemStore())
}

func TestPgOutboundAIPriceTiers(t *testing.T) {
	s, _, _ := pgWithPool(t)
	checkPriceTiers(t, s)
}
