package outbound

// adr: 20261019072722 — audio token prices round-trip through Postgres to
// the resolver, and settled audio tokens roll up by day within their input
// and output totals.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAIAudioTokens(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	inputAudio, cachedAudio, outputAudio := int64(2_855_614), int64(35_696), int64(5_711_227)
	if err := f.store.SetOutboundAIPrices(ctx, f.account.ID, f.offer.ID, []api.OutboundAIPriceEntry{{ModelPrefix: "gpt-realtime",
		InputMillicentsPerMTok: 356_952, OutputMillicentsPerMTok: 1_427_807, InputAudioMillicentsPerMTok: &inputAudio,
		CachedInputAudioMillicentsPerMTok: &cachedAudio, OutputAudioMillicentsPerMTok: &outputAudio}}); err != nil {
		t.Fatal(err)
	}
	prices, err := f.store.GetOutboundAIPrices(ctx, f.account.ID, f.offer.ID)
	if err != nil || len(prices.Entries) != 1 || *prices.Entries[0].InputAudioMillicentsPerMTok != inputAudio ||
		*prices.Entries[0].CachedInputAudioMillicentsPerMTok != cachedAudio || *prices.Entries[0].OutputAudioMillicentsPerMTok != outputAudio {
		t.Fatalf("stored prices = %+v, %v", prices, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_price_lists SET effective_from = now() - interval '1 minute' WHERE integration_id = $1`,
		f.offer.ID); err != nil {
		t.Fatal(err)
	}
	integration, err := f.integration(f.offer.ID)
	if err != nil || len(integration.AIPrices) != 1 || integration.AIPrices[0].InputAudio != inputAudio ||
		integration.AIPrices[0].CachedInputAudio != cachedAudio || integration.AIPrices[0].OutputAudio != outputAudio {
		t.Fatalf("resolved prices = %+v, %v", integration.AIPrices, err)
	}
	usage := AIUsage{Model: "gpt-realtime", Known: true, InputTokens: 1000, OutputTokens: 500, InputAudioTokens: 700, OutputAudioTokens: 400}
	cost := aiCallCostMillicents(integration, usage)
	for range 2 {
		if _, err := backend.RecordAIUsageAlerts(ctx, AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID,
			Outcome: AIUsageOutcomeKnown, Usage: usage, CostMillicents: cost}); err != nil {
			t.Fatal(err)
		}
	}
	day, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range day.Items {
		if item.Model == "gpt-realtime" {
			found = true
			if item.InputAudioTokens != 1400 || item.OutputAudioTokens != 800 || item.InputTokens != 2000 || item.CostMillicents != 2*cost {
				t.Fatalf("audio usage = %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("no gpt-realtime usage in %+v", day.Items)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_usage_daily SET input_audio_tokens = input_tokens + 1 WHERE integration_id = $1`,
		f.offer.ID); err == nil {
		t.Fatal("more audio input tokens than input tokens were stored")
	}
}
