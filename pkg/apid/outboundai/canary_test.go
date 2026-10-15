package outboundai

// adr: 1340 — a route's canary is started, read, promoted and stopped
// through apid; content policies accept a moderation check only on the
// account's own AI integrations; and the AI policy stores log export.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func serveRoute(handler func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, method, routeID, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/outbound/ai-routes/"+routeID+"/canary", strings.NewReader(body))
	r.SetPathValue("route", routeID)
	w := httptest.NewRecorder()
	handler(w, r, acct)
	return w
}

// aiIntegration adds an enabled customer AI integration to the account.
func aiIntegration(t *testing.T, store *state.MemStore, accountID, provider string) string {
	t.Helper()
	ctx := context.Background()
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: accountID, Name: "llm-" + uuid.NewString()[:6], Origin: "https://api.example.com",
		AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer",
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 10, Burst: 10, MaxInFlight: 10, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundAIPolicy(ctx, accountID, offer.ID, state.OutboundAIPolicy{Provider: provider}); err != nil {
		t.Fatal(err)
	}
	return offer.ID
}

func TestRouteCanaryEndpoints(t *testing.T) {
	service, store, acct, _ := newTestService(t)
	ctx := context.Background()
	first, second := aiIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI), aiIntegration(t, store, acct.ID, api.OutboundAIProviderOpenAI)
	route, err := store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: acct.ID, Name: "chat",
		Targets: []state.OutboundAIRouteTarget{{IntegrationID: first}, {IntegrationID: second, Model: "gpt-new"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"target":1,"percent":10}`, `{"target":2,"percent":51}`, `{"target":2,"percent":10,"min_calls":5}`,
		`{"target":3,"percent":10}`, `{"target":2,"percent":10,"max_latency_increase_percent":0}`} {
		if w := serveRoute(service.PutRouteCanary, acct, http.MethodPut, route.ID, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := serveRoute(service.GetRouteCanary, acct, http.MethodGet, route.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("GET without a canary = %d", w.Code)
	}
	w := serveRoute(service.PutRouteCanary, acct, http.MethodPut, route.ID, `{"target":2,"percent":10,"max_latency_increase_percent":50}`)
	var canary api.OutboundAIRouteCanary
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &canary) != nil || canary.State != api.OutboundAIRouteCanaryActive ||
		canary.MinCalls != api.OutboundAIRouteCanaryMinCallsDefault || canary.WindowMinutes != api.OutboundAIRouteCanaryWindowMinutesDefault ||
		canary.MaxErrorRateIncrease != api.OutboundAIRouteCanaryErrorIncreaseDefault || *canary.MaxLatencyIncreasePercent != 50 {
		t.Fatalf("PUT canary = %d %s", w.Code, w.Body.String())
	}
	w = serveRoute(service.GetRouteCanary, acct, http.MethodGet, route.ID, "")
	var status api.OutboundAIRouteCanaryStatus
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Baseline.Target != 1 ||
		status.CanaryStats.Target != 2 || status.CanaryStats.MeanTTFBMS != nil || !strings.Contains(w.Body.String(), `"mean_ttfb_ms":null`) {
		t.Fatalf("GET canary = %d %s", w.Code, w.Body.String())
	}
	w = serveRoute(service.GetRoute, acct, http.MethodGet, route.ID, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"canary":{"target":2`) {
		t.Fatalf("GET route = %d %s", w.Code, w.Body.String())
	}
	w = serveRoute(service.PromoteRouteCanary, acct, http.MethodPost, route.ID, "")
	var promoted api.OutboundAIRoute
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &promoted) != nil || promoted.Canary != nil ||
		promoted.Targets[0].IntegrationID != second || promoted.Targets[0].Model != "gpt-new" || promoted.Targets[1].IntegrationID != first {
		t.Fatalf("promote = %d %s", w.Code, w.Body.String())
	}
	if w := serveRoute(service.DeleteRouteCanary, acct, http.MethodDelete, route.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("DELETE without a canary = %d", w.Code)
	}
	if w := serveRoute(service.PutRouteCanary, acct, http.MethodPut, route.ID, `{"target":2,"percent":5}`); w.Code != http.StatusOK {
		t.Fatalf("second PUT = %d", w.Code)
	}
	if w := serveRoute(service.DeleteRouteCanary, acct, http.MethodDelete, route.ID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", w.Code)
	}
	other, err := store.CreateAccount(ctx, "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if w := serveRoute(service.PutRouteCanary, other, http.MethodPut, route.ID, `{"target":2,"percent":5}`); w.Code != http.StatusNotFound {
		t.Fatalf("another account's PUT = %d", w.Code)
	}
}

func TestContentPolicyModeration(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	ctx := context.Background()
	if err := store.SetOutboundAIPolicy(ctx, acct.ID, integrationID, state.OutboundAIPolicy{Provider: api.OutboundAIProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	anthropic := aiIntegration(t, store, acct.ID, api.OutboundAIProviderAnthropic)
	other, err := store.CreateAccount(ctx, "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign := aiIntegration(t, store, other.ID, api.OutboundAIProviderOpenAI)
	put := func(moderation string) int {
		return serve(service.PutContentPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai-content-policy", integrationID,
			`{"moderation":`+moderation+`}`).Code
	}
	for _, tc := range []struct {
		moderation string
		want       int
	}{
		{`{"action":"block","kind":"classifier","integration_id":"` + anthropic + `","model":"claude-haiku","categories":["prompt_injection"],"on_error":"block"}`, http.StatusNoContent},
		{`{"action":"flag","kind":"openai_moderation","integration_id":"` + integrationID + `","categories":["hate","violence"]}`, http.StatusNoContent},
		{`{"action":"block","kind":"openai_moderation","integration_id":"` + anthropic + `","categories":["hate"]}`, http.StatusBadRequest},
		{`{"action":"block","kind":"openai_moderation","integration_id":"` + integrationID + `","categories":["prompt_injection"]}`, http.StatusBadRequest},
		{`{"action":"block","kind":"classifier","integration_id":"` + anthropic + `","categories":["hate"]}`, http.StatusBadRequest},
		{`{"action":"block","kind":"classifier","integration_id":"` + foreign + `","model":"m","categories":["hate"]}`, http.StatusBadRequest},
		{`{"action":"block","kind":"classifier","integration_id":"` + anthropic + `","model":"m","categories":["hate","hate"]}`, http.StatusBadRequest},
		{`{"action":"block","kind":"classifier","integration_id":"` + anthropic + `","model":"m","categories":["spam"]}`, http.StatusBadRequest},
		{`{"action":"maybe"}`, http.StatusBadRequest},
		{`{"action":"off"}`, http.StatusNoContent},
	} {
		if got := put(tc.moderation); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.moderation, got, tc.want)
		}
	}
	if got := put(`{"action":"flag","kind":"openai_moderation","integration_id":"` + integrationID + `","categories":["hate"]}`); got != http.StatusNoContent {
		t.Fatalf("flag policy = %d", got)
	}
	offers, err := store.ListOutboundIntegrationOffers(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, offer := range offers {
		if offer.ID == integrationID && (offer.AIContentPolicy.Moderation.Action != api.OutboundAIModerationFlag ||
			offer.AIContentPolicy.Moderation.OnError != api.OutboundAIModerationOnErrorAllow) {
			t.Fatalf("stored moderation = %+v", offer.AIContentPolicy.Moderation)
		}
	}
}

func TestPutPolicyStoresLogExport(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	if w := serve(service.PutPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai-policy", integrationID,
		`{"ai_provider":"","log_export":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("export without a provider = %d", w.Code)
	}
	if w := serve(service.PutPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai-policy", integrationID,
		`{"ai_provider":"openai","log_export":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("export = %d %s", w.Code, w.Body.String())
	}
	offers, err := store.ListOutboundIntegrationOffers(context.Background(), acct.ID)
	if err != nil || len(offers) != 1 || !offers[0].AILogExport {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
}
