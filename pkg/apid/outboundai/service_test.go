package outboundai

// adr: 994 — the catalog endpoint lists or resolves a provider's prices,
// the AI policy stores per-end-user limits and the users endpoint pages one
// day's end users, most expensive first.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type testHost struct{ store *state.MemStore }

func (h testHost) LoadApp(w http.ResponseWriter, _ *http.Request, _ state.Account, _ string) (state.App, bool) {
	http.NotFound(w, nil)
	return state.App{}, false
}

func (h testHost) Store() state.Store { return h.store }

func (h testHost) SealCredential(w http.ResponseWriter, _ string) ([]byte, bool) {
	problem(w, http.StatusServiceUnavailable, "outbound_credential_seal_unavailable", "Credential encryption is unavailable")
	return nil, false
}

func newTestService(t *testing.T) (*Service, *state.MemStore, state.Account, string) {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "ai-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: acct.ID, Name: "llm", Origin: "https://api.openai.com",
		AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer",
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 10, Burst: 10, MaxInFlight: 10, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Host: testHost{store}}, store, acct, offer.ID
}

func serve(handler func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, method, target, integrationID, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.SetPathValue("integration", integrationID)
	w := httptest.NewRecorder()
	handler(w, r, acct)
	return w
}

func TestGetPriceCatalog(t *testing.T) {
	service, _, acct, _ := newTestService(t)
	all := serve(service.GetPriceCatalog, acct, http.MethodGet, "/v1/outbound/ai-price-catalog?provider=anthropic", "", "")
	var list api.OutboundAIPriceCatalogResponse
	if all.Code != http.StatusOK || json.Unmarshal(all.Body.Bytes(), &list) != nil || len(list.Entries) < 5 || list.Currency != api.BillingCurrency || list.USDPerEUR != "1" {
		t.Fatalf("catalog = %d %s", all.Code, all.Body.String())
	}
	one := serve(service.GetPriceCatalog, acct, http.MethodGet, "/v1/outbound/ai-price-catalog?provider=openai&model=gpt-4o-2024-08-06", "", "")
	var match api.OutboundAIPriceCatalogResponse
	if one.Code != http.StatusOK || json.Unmarshal(one.Body.Bytes(), &match) != nil || len(match.Entries) != 1 ||
		match.Entries[0].InputMillicentsPerMTok == 0 || match.Entries[0].CachedInputMillicentsPerMTok == nil {
		t.Fatalf("model lookup = %d %s", one.Code, one.Body.String())
	}
	none := serve(service.GetPriceCatalog, acct, http.MethodGet, "/v1/outbound/ai-price-catalog?provider=openai&model=no-such-model", "", "")
	if none.Code != http.StatusOK || !strings.Contains(none.Body.String(), `"entries":[]`) {
		t.Fatalf("unknown model = %d %s", none.Code, none.Body.String())
	}
	for _, query := range []string{"", "?provider=mistral", "?provider=openai&model=" + strings.Repeat("m", 129)} {
		if got := serve(service.GetPriceCatalog, acct, http.MethodGet, "/v1/outbound/ai-price-catalog"+query, "", ""); got.Code != http.StatusBadRequest {
			t.Errorf("catalog %q = %d", query, got.Code)
		}
	}
}

func TestPutPolicyStoresEndUserLimits(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	put := func(body string) int {
		return serve(service.PutPolicy, acct, http.MethodPut, "/v1/outbound/integrations/x/ai", integrationID, body).Code
	}
	if got := put(`{"ai_provider":"openai","user_daily_token_limit":5000,"user_daily_cost_limit_millicents":250000}`); got != http.StatusNoContent {
		t.Fatalf("put policy = %d", got)
	}
	offers, err := store.ListOutboundIntegrationOffers(context.Background(), acct.ID)
	if err != nil || len(offers) != 1 || offers[0].AIUserDailyTokenLimit == nil || *offers[0].AIUserDailyTokenLimit != 5000 ||
		offers[0].AIUserDailyCostLimit == nil || *offers[0].AIUserDailyCostLimit != 250000 {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	for _, body := range []string{`{"user_daily_token_limit":5}`, `{"ai_provider":"openai","user_daily_token_limit":0}`,
		`{"ai_provider":"openai","user_daily_cost_limit_millicents":1000000000001}`} {
		if got := put(body); got != http.StatusBadRequest {
			t.Errorf("put %s = %d", body, got)
		}
	}
}

func TestGetUserUsage(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	today := time.Now().UTC().Format("2006-01-02")
	store.SeedOutboundAIUserUsage(integrationID, today,
		state.OutboundAIUserUsageItem{EndUser: "cheap", Requests: 1, InputTokens: 10, OutputTokens: 5, CostMillicents: 3},
		state.OutboundAIUserUsageItem{EndUser: "dear", Requests: 2, InputTokens: 100, OutputTokens: 50, CostMillicents: 90},
		state.OutboundAIUserUsageItem{EndUser: api.OutboundAIUserOverflow, Requests: 4, InputTokens: 1, CostMillicents: 1})
	get := func(query string) (*httptest.ResponseRecorder, api.OutboundAIUserUsageResponse) {
		w := serve(service.GetUserUsage, acct, http.MethodGet, "/v1/outbound/integrations/x/ai-usage/users"+query, integrationID, "")
		var out api.OutboundAIUserUsageResponse
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w, out
	}
	w, page := get("?limit=2")
	if w.Code != http.StatusOK || page.TrackedUsers != 3 || len(page.Items) != 2 || page.Items[0].EndUser != "dear" ||
		page.Items[0].Tokens != 150 || page.Items[1].EndUser != "cheap" {
		t.Fatalf("users page = %d %s", w.Code, w.Body.String())
	}
	if w, one := get("?end_user=" + api.OutboundAIUserOverflow); w.Code != http.StatusOK || len(one.Items) != 1 || one.Items[0].Requests != 4 {
		t.Fatalf("overflow row = %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{"?limit=0", "?limit=1001", "?end_user=has%20space", "?date=2001-01-01", "?date=tomorrow"} {
		if w, _ := get(query); w.Code != http.StatusBadRequest {
			t.Errorf("users %q = %d", query, w.Code)
		}
	}
	other := serve(service.GetUserUsage, state.Account{ID: uuid.NewString()}, http.MethodGet, "/v1/outbound/integrations/x/ai-usage/users", integrationID, "")
	if other.Code != http.StatusNotFound {
		t.Fatalf("another account read the usage: %d", other.Code)
	}
}
