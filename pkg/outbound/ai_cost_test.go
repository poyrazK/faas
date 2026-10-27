package outbound

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/aiprices"
)

func int64Ref(v int64) *int64 { return &v }

func TestAICostMillicentsPricesByLongestPrefix(t *testing.T) {
	prices := ResolveAIPrices([]api.OutboundAIPriceEntry{
		{ModelPrefix: "*", InputMillicentsPerMTok: 1_000, OutputMillicentsPerMTok: 1_000},
		{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: 250_000, OutputMillicentsPerMTok: 1_000_000},
		{ModelPrefix: "gpt-4o-mini", InputMillicentsPerMTok: 15_000, CachedInputMillicentsPerMTok: int64Ref(7_500), OutputMillicentsPerMTok: 60_000},
		{ModelPrefix: "claude-sonnet", InputMillicentsPerMTok: 300_000, CachedInputMillicentsPerMTok: int64Ref(30_000),
			CacheWriteMillicentsPerMTok: int64Ref(375_000), OutputMillicentsPerMTok: 1_500_000},
	})
	cases := []struct {
		name  string
		usage AIUsage
		want  int64
	}{
		// 1M input at 15,000 + 1M output at 60,000 per million.
		{"exact prefix", AIUsage{Model: "gpt-4o-mini-2024-07-18", Known: true, InputTokens: 1_000_000, OutputTokens: 1_000_000}, 75_000},
		// 400k uncached at 15,000 + 600k cached at 7,500 per million.
		{"cached input", AIUsage{Model: "gpt-4o-mini", Known: true, InputTokens: 1_000_000, CachedInputTokens: 600_000}, 6_000 + 4_500},
		{"shorter prefix", AIUsage{Model: "gpt-4o-2024-11-20", Known: true, InputTokens: 2_000_000}, 500_000},
		// 1,000 uncached + 2,000 cache reads + 500 cache writes + 1,000 output.
		{"anthropic cache", AIUsage{Model: "claude-sonnet-5", Known: true, InputTokens: 3_500, CachedInputTokens: 2_000, CacheWriteTokens: 500, OutputTokens: 1_000},
			300 + 60 + 188 + 1_500},
		{"wildcard", AIUsage{Model: "llama-3", Known: true, InputTokens: 10}, 1},
		{"unknown usage is free", AIUsage{Model: "gpt-4o", InputTokens: 1_000_000}, 0},
	}
	for _, tc := range cases {
		if got := aiCostMillicents(prices, tc.usage); got != tc.want {
			t.Errorf("%s: cost = %d, want %d", tc.name, got, tc.want)
		}
	}
	if got := aiCostMillicents(nil, AIUsage{Model: "m", Known: true, InputTokens: 5}); got != 0 {
		t.Fatalf("no price list cost %d", got)
	}
	huge := ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "*", InputMillicentsPerMTok: api.MaxOutboundAIPriceMillicentsPerMTok, OutputMillicentsPerMTok: api.MaxOutboundAIPriceMillicentsPerMTok}})
	if got := aiCostMillicents(huge, AIUsage{Model: "m", Known: true, InputTokens: 3 * api.OutboundAIMaxTokensPerResponse, OutputTokens: api.OutboundAIMaxTokensPerResponse}); got != api.MaxOutboundAICostMillicents {
		t.Fatalf("overflow-sized cost = %d, want clamp %d", got, api.MaxOutboundAICostMillicents)
	}
}

// adr: 994 — models missing from the customer's list price from the
// built-in catalog; the customer's list, including "*", always wins.
func TestAICallCostFallsBackToCatalog(t *testing.T) {
	integration := Integration{AIProvider: api.OutboundAIProviderOpenAI}
	usage := AIUsage{Model: "gpt-4o-mini-2024-07-18", Known: true, InputTokens: 1_000_000, OutputTokens: 1_000_000}
	catalogEntry, ok := aiprices.Default().Lookup(api.OutboundAIProviderOpenAI, usage.Model)
	if !ok {
		t.Fatal("the catalog does not price gpt-4o-mini")
	}
	if got, want := aiCallCostMillicents(integration, usage), catalogEntry.Price.Input+catalogEntry.Price.Output; got != want || got == 0 {
		t.Fatalf("catalog cost = %d, want %d", got, want)
	}
	integration.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "gpt-4o-mini", InputMillicentsPerMTok: 1, OutputMillicentsPerMTok: 2}})
	if got := aiCallCostMillicents(integration, usage); got != 3 {
		t.Fatalf("customer price cost = %d, want 3", got)
	}
	integration.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "*", InputMillicentsPerMTok: 0, OutputMillicentsPerMTok: 0}})
	if got := aiCallCostMillicents(integration, usage); got != 0 {
		t.Fatalf("a zero wildcard did not turn the catalog off: %d", got)
	}
	if got := aiCallCostMillicents(Integration{AIProvider: api.OutboundAIProviderOpenAI}, AIUsage{Model: "unlisted-model", Known: true, InputTokens: 9}); got != 0 {
		t.Fatalf("an unlisted model cost %d", got)
	}
}

func TestValidOutboundAIPriceEntries(t *testing.T) {
	valid := []api.OutboundAIPriceEntry{{ModelPrefix: "*", InputMillicentsPerMTok: 1, OutputMillicentsPerMTok: 2}}
	if !api.ValidOutboundAIPriceEntries(valid) || !api.ValidOutboundAIPriceEntries(nil) {
		t.Fatal("valid price list rejected")
	}
	for name, entries := range map[string][]api.OutboundAIPriceEntry{
		"duplicate prefix": {valid[0], valid[0]},
		"negative price":   {{ModelPrefix: "m", InputMillicentsPerMTok: -1}},
		"price too high":   {{ModelPrefix: "m", OutputMillicentsPerMTok: api.MaxOutboundAIPriceMillicentsPerMTok + 1}},
		"empty prefix":     {{ModelPrefix: ""}},
		"control char":     {{ModelPrefix: "m\n"}},
	} {
		if api.ValidOutboundAIPriceEntries(entries) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestPerAppTokenLimitAndDailyCostLimit(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o","usage":{"prompt_tokens":40,"completion_tokens":10}}`))
	}))
	defer upstream.Close()
	build := func(configure func(*Integration)) (*Handler, *MemoryBackend) {
		integration, err := NewIntegration("integration-1", upstream.URL, "token", []string{"app-1", "app-2"}, 100, 10, 10, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		integration.AIProvider = api.OutboundAIProviderOpenAI
		configure(&integration)
		resolver, err := NewStaticResolver([]Integration{integration})
		if err != nil {
			t.Fatal(err)
		}
		backend := NewMemoryBackend()
		handler, err := NewHandler(resolver, backend, upstream.Client())
		if err != nil {
			t.Fatal(err)
		}
		return handler, backend
	}
	call := func(handler *Handler, app string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		// A small output ceiling keeps the ADR-991 reservation below the
		// limits under test.
		// The model is named, as it must be under a cost cap (ADR 20261027132936).
		handler.ServeHTTP(recorder, gatewayRequest("/i/integration-1/v1/chat/completions", "token", app, http.MethodPost, strings.NewReader(`{"model":"gpt-4o","max_tokens":5}`)))
		return recorder
	}

	perApp, _ := build(func(i *Integration) {
		i.BindingAppIDs = map[string]struct{}{"app-1": {}, "app-2": {}}
		i.BindingDailyAITokenLimits = map[string]*int64{"app-1": int64Ref(50)}
	})
	if got := call(perApp, "app-1").Code; got != http.StatusOK {
		t.Fatalf("first app-1 call = %d", got)
	}
	limited := call(perApp, "app-1")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("X-Gregale-Outbound-Rejection") != ReasonDailyTokenLimit {
		t.Fatalf("app-1 over its own limit = %d %q", limited.Code, limited.Header().Get("X-Gregale-Outbound-Rejection"))
	}
	if got := call(perApp, "app-2").Code; got != http.StatusOK {
		t.Fatalf("app-2 was limited by app-1's budget: %d", got)
	}

	// 40 input at 250,000 and 10 output at 1,000,000 per million: 10 + 10.
	priced, backend := build(func(i *Integration) {
		i.AIPrices = ResolveAIPrices([]api.OutboundAIPriceEntry{{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: 250_000, OutputMillicentsPerMTok: 1_000_000}})
		i.DailyAICostLimit = int64Ref(40)
	})
	for i := 0; i < 2; i++ {
		if got := call(priced, "app-1").Code; got != http.StatusOK {
			t.Fatalf("priced call %d = %d", i, got)
		}
	}
	if daily, monthly := backend.AICost("integration-1"); daily != 40 || monthly != 40 {
		t.Fatalf("settled cost daily=%d monthly=%d, want 40", daily, monthly)
	}
	over := call(priced, "app-1")
	if over.Code != http.StatusTooManyRequests || over.Header().Get("X-Gregale-Outbound-Rejection") != ReasonDailyCostLimit {
		t.Fatalf("over cost limit = %d %q", over.Code, over.Header().Get("X-Gregale-Outbound-Rejection"))
	}
	if row, _ := backend.AIUsageRollup("integration-1", "app-1", "gpt-4o"); row.CostMillicents != 40 {
		t.Fatalf("rollup cost = %d", row.CostMillicents)
	}
}

func TestAIBudgetExhaustedResetsAtMonthEnd(t *testing.T) {
	now := time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)
	reason, retry, limited := aiBudgetExhausted(now, aiBudgetState{monthlyCost: 100}, aiBudgetLimits{monthlyCost: int64Ref(100)}, aiReservation{})
	if !limited || reason != ReasonMonthlyCostLimit || retry != time.Hour {
		t.Fatalf("monthly limit = %v %s %s", limited, reason, retry)
	}
	if _, _, limited := aiBudgetExhausted(now, aiBudgetState{dailyTokens: 99}, aiBudgetLimits{dailyTokens: int64Ref(100)}, aiReservation{}); limited {
		t.Fatal("limit applied below its threshold")
	}
}
