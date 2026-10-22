package outbound_test

// adr: 997 — Postgres stores media prices with a price list, resolves them
// for outboundd, and rolls up images, audio seconds and characters.

import (
	"context"
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

func TestPostgresAIMediaPricesAndRollup(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-ai-media-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "ai-media",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodPost},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	perImage, perMinute, perMChars, outputImage := int64(4000), int64(600), int64(1_500_000), int64(3_500_000)
	entries := []api.OutboundAIPriceEntry{
		{ModelPrefix: "dall-e-3", MillicentsPerImage: &perImage},
		{ModelPrefix: "gpt-image-1", InputMillicentsPerMTok: 450_000, OutputMillicentsPerMTok: 0, OutputImageMillicentsPerMTok: &outputImage},
		{ModelPrefix: "whisper-1", MillicentsPerAudioMinute: &perMinute},
		{ModelPrefix: "tts-1", MillicentsPerMillionCharacters: &perMChars},
	}
	if err := store.SetOutboundAIPrices(ctx, account.ID, offer.ID, entries); err != nil {
		t.Fatal(err)
	}
	tooDear := int64(api.MaxOutboundAIPriceMillicentsPerImage + 1)
	if err := store.SetOutboundAIPrices(ctx, account.ID, offer.ID, []api.OutboundAIPriceEntry{{ModelPrefix: "x", MillicentsPerImage: &tooDear}}); err == nil {
		t.Fatal("a per-image price over the ceiling was stored")
	}
	list, err := store.GetOutboundAIPrices(ctx, account.ID, offer.ID)
	if err != nil || len(list.Entries) != 4 || list.Entries[0].ModelPrefix != "dall-e-3" || list.Entries[0].MillicentsPerImage == nil ||
		*list.Entries[0].MillicentsPerImage != perImage || list.Entries[1].OutputImageMillicentsPerMTok == nil ||
		list.Entries[3].MillicentsPerAudioMinute == nil || list.Entries[2].MillicentsPerMillionCharacters == nil {
		t.Fatalf("price list = %+v, %v", list, err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	integration, err := resolver.Integration(ctx, offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	prices := map[string]outbound.AIPrice{}
	for _, price := range integration.AIPrices {
		prices[price.ModelPrefix] = price
	}
	if prices["dall-e-3"].Image != perImage || prices["gpt-image-1"].OutputImage != outputImage || prices["gpt-image-1"].InputImage != 450_000 ||
		prices["whisper-1"].AudioMinute != perMinute || prices["tts-1"].Characters != perMChars {
		t.Fatalf("resolved prices = %+v", prices)
	}
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, usage := range []outbound.AIUsage{
		{Model: "dall-e-3", Known: true, Images: 2},
		{Model: "whisper-1", Known: true, AudioSeconds: 90},
		{Model: "tts-1", Known: true, Characters: 11},
	} {
		if err := backend.RecordAIUsage(ctx, outbound.AIUsageRecord{IntegrationID: offer.ID, Caller: outbound.AIUsageCallerRuns,
			Outcome: outbound.AIUsageOutcomeKnown, Usage: usage, CostMillicents: 1}); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := store.GetOutboundAIUsage(ctx, account.ID, offer.ID, "")
	if err != nil || len(usage.Items) != 3 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	media := map[string]state.OutboundAIUsageItem{}
	for _, item := range usage.Items {
		media[item.Model] = item
	}
	if media["dall-e-3"].Images != 2 || media["whisper-1"].AudioSeconds != 90 || media["tts-1"].Characters != 11 {
		t.Fatalf("media rollups = %+v", media)
	}
}
