package outbound

// adr: 1432 — allow_unmetered and per-second video prices round-trip
// through Postgres to the resolver, and settled video seconds roll up.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAIUnmeteredPolicyAndVideoUsage(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetOutboundAIPolicy(ctx, f.account.ID, f.offer.ID, state.OutboundAIPolicy{AllowUnmetered: true}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("allow_unmetered without a provider = %v", err)
	}
	refused := f.callPath(ctx, f.handler(nil, nil), "/v1/realtime/client_secrets", `{}`)
	if refused.Code != http.StatusForbidden || refused.Header().Get(AIUnmeteredHeader) != "" {
		t.Fatalf("an unmetered write = %d %s", refused.Code, refused.Body.String())
	}
	if err := f.store.SetOutboundAIPolicy(ctx, f.account.ID, f.offer.ID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI,
		AllowUnmetered: true}); err != nil {
		t.Fatal(err)
	}
	video := int64(2_500)
	if err := f.store.SetOutboundAIPrices(ctx, f.account.ID, f.offer.ID, []api.OutboundAIPriceEntry{
		{ModelPrefix: "veo-custom@4k", MillicentsPerVideoSecond: &video}}); err != nil {
		t.Fatal(err)
	}
	prices, err := f.store.GetOutboundAIPrices(ctx, f.account.ID, f.offer.ID)
	if err != nil || len(prices.Entries) != 1 || prices.Entries[0].MillicentsPerVideoSecond == nil || *prices.Entries[0].MillicentsPerVideoSecond != video {
		t.Fatalf("stored prices = %+v, %v", prices, err)
	}
	offers, err := f.store.ListOutboundIntegrationOffers(ctx, f.account.ID)
	if err != nil || len(offers) == 0 || !offers[0].AIAllowUnmetered {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	// The resolver reads the price list once it is in effect.
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_price_lists SET effective_from = now() - interval '1 minute' WHERE integration_id = $1`,
		f.offer.ID); err != nil {
		t.Fatal(err)
	}
	integration, err := f.integration(f.offer.ID)
	if err != nil || !integration.AIAllowUnmetered || len(integration.AIPrices) != 1 || integration.AIPrices[0].VideoSecond != video {
		t.Fatalf("resolved integration = %+v, %v", integration, err)
	}
	allowed := f.callPath(ctx, f.handler(nil, nil), "/v1/realtime/client_secrets", `{}`)
	if allowed.Code != http.StatusOK || allowed.Header().Get(AIUnmeteredHeader) != "allowed" {
		t.Fatalf("an allowed unmetered write = %d %s", allowed.Code, allowed.Body.String())
	}
	usage := AIUsage{Model: "veo-custom", Known: true, VideoSeconds: 8, VideoResolution: "4k"}
	if _, err := backend.RecordAIUsageAlerts(ctx, AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown,
		Usage: usage, CostMillicents: aiCallCostMillicents(integration, usage)}); err != nil {
		t.Fatal(err)
	}
	day, err := f.store.GetOutboundAIUsage(ctx, f.account.ID, f.offer.ID, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range day.Items {
		if item.Model == "veo-custom" {
			found = item.VideoSeconds == 8 && item.CostMillicents == 8*video
		}
	}
	if !found {
		t.Fatalf("video usage = %+v", day.Items)
	}
}

// callPath sends one POST to a provider path as the bound app through the
// relay.
func (f *aiPreviewFixture) callPath(ctx context.Context, handler *Handler, providerPath, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	ctx = withRelayPrincipal(ctx, relayPrincipal{app: WorkloadIdentity{AccountID: f.account.ID, AppID: f.app.ID}})
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://outboundd"+Prefix+f.offer.ID+providerPath,
		strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
