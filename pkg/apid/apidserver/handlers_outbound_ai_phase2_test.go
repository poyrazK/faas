package apidserver_test

// adr: 990 — per-app AI limits, price lists, cost budgets and AI routes.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedAIIntegration(t *testing.T, e apidserver.TestEnv, name, provider string) string {
	t.Helper()
	// ADR-991: the AI gateway APIs are closed until the release gate opens.
	(*e.S()).EnablePlatformFlagForAll(platformflags.AIGateway)
	id := uuid.NewString()
	(*e.Store()).SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{
		ID: id, AccountID: (*e.Acct()).ID, Name: name, Origin: "https://api.example.com",
		AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"},
		Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed",
	})
	if provider != "" {
		if err := (*e.Store()).SetOutboundAIPolicy(context.Background(), (*e.Acct()).ID, id, state.OutboundAIPolicy{Provider: provider}); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestOutboundAICostPolicyAndPrices(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := seedAIIntegration(t, e, "llm", api.OutboundAIProviderOpenAI)
	policyPath := "/v1/outbound/integrations/" + id + "/ai"
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, policyPath, api.PutOutboundAIPolicyRequest{DailyCostLimitMillicents: outboundAITokens(5)}, nil),
		http.StatusBadRequest, api.CodeValidation)
	set := e.Do(t, http.MethodPut, policyPath, api.PutOutboundAIPolicyRequest{
		AIProvider: api.OutboundAIProviderOpenAI, DailyCostLimitMillicents: outboundAITokens(500_000), MonthlyCostLimitMillicents: outboundAITokens(9_000_000),
	}, nil)
	if set.Code != http.StatusNoContent {
		t.Fatalf("set cost policy = %d %s", set.Code, set.Body.String())
	}
	offer := firstOutboundOffer(t, e)
	if offer.AIDailyCostLimitMillicents == nil || *offer.AIDailyCostLimitMillicents != 500_000 || offer.AIMonthlyCostLimitMillicents == nil {
		t.Fatalf("offer cost limits = %+v", offer)
	}

	pricesPath := "/v1/outbound/integrations/" + id + "/ai-prices"
	empty := e.Do(t, http.MethodGet, pricesPath, nil, nil)
	var list api.OutboundAIPriceListResponse
	if empty.Code != http.StatusOK || json.Unmarshal(empty.Body.Bytes(), &list) != nil || list.EffectiveFrom != nil || len(list.Entries) != 0 {
		t.Fatalf("empty price list = %d %s", empty.Code, empty.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, pricesPath, api.PutOutboundAIPricesRequest{Entries: []api.OutboundAIPriceEntry{
		{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: -1},
	}}, nil), http.StatusBadRequest, api.CodeValidation)
	put := e.Do(t, http.MethodPut, pricesPath, api.PutOutboundAIPricesRequest{Entries: []api.OutboundAIPriceEntry{
		{ModelPrefix: "gpt-4o", InputMillicentsPerMTok: 250_000, CachedInputMillicentsPerMTok: outboundAITokens(125_000), OutputMillicentsPerMTok: 1_000_000},
		{ModelPrefix: "*", InputMillicentsPerMTok: 100_000, OutputMillicentsPerMTok: 400_000},
	}}, nil)
	if put.Code != http.StatusNoContent {
		t.Fatalf("put prices = %d %s", put.Code, put.Body.String())
	}
	got := e.Do(t, http.MethodGet, pricesPath, nil, nil)
	if json.Unmarshal(got.Body.Bytes(), &list) != nil || list.EffectiveFrom == nil || len(list.Entries) != 2 {
		t.Fatalf("price list = %s", got.Body.String())
	}

	today := time.Now().UTC().Format("2006-01-02")
	(*e.Store()).SeedOutboundAIUsage(id, today, state.OutboundAIUsageItem{Caller: "runs", Model: "gpt-4o", Requests: 2, InputTokens: 100, OutputTokens: 20, CostMillicents: 45})
	usage := e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-usage", nil, nil)
	var body api.OutboundAIUsageResponse
	if json.Unmarshal(usage.Body.Bytes(), &body) != nil || body.DailyCostMillicents != 45 || body.MonthlyCostMillicents < 45 ||
		body.Month != today[:7] || body.DailyCostLimitMillicents == nil || len(body.Items) != 1 || body.Items[0].CostMillicents != 45 {
		t.Fatalf("usage with cost = %s", usage.Body.String())
	}
}

func TestOutboundBindingAIBudget(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.CreateApp(t, e, "ai-budget-app")
	id := seedAIIntegration(t, e, "llm", api.OutboundAIProviderAnthropic)
	bindingPath := "/v1/apps/" + app.Slug + "/outbound-bindings/" + id
	if bound := e.Do(t, http.MethodPut, bindingPath, nil, nil); bound.Code != http.StatusOK {
		t.Fatalf("bind = %d %s", bound.Code, bound.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, bindingPath+"/ai-budget", api.PutOutboundBindingAIBudgetRequest{DailyTokenLimit: outboundAITokens(0)}, nil),
		http.StatusBadRequest, api.CodeValidation)
	set := e.Do(t, http.MethodPut, bindingPath+"/ai-budget", api.PutOutboundBindingAIBudgetRequest{DailyTokenLimit: outboundAITokens(25_000)}, nil)
	if set.Code != http.StatusNoContent {
		t.Fatalf("set app budget = %d %s", set.Code, set.Body.String())
	}
	var bindings api.OutboundAppBindingList
	list := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/outbound-bindings", nil, nil)
	if json.Unmarshal(list.Body.Bytes(), &bindings) != nil || len(bindings.Items) != 1 ||
		bindings.Items[0].AIDailyTokenLimit == nil || *bindings.Items[0].AIDailyTokenLimit != 25_000 {
		t.Fatalf("binding AI limit = %s", list.Body.String())
	}
	unbound := seedAIIntegration(t, e, "other-llm", api.OutboundAIProviderAnthropic)
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/outbound-bindings/"+unbound+"/ai-budget",
		api.PutOutboundBindingAIBudgetRequest{DailyTokenLimit: outboundAITokens(5)}, nil), http.StatusNotFound, api.CodeNotFound)
}

func TestOutboundAIRoutesLifecycle(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	primary := seedAIIntegration(t, e, "primary", api.OutboundAIProviderOpenAI)
	backup := seedAIIntegration(t, e, "backup", api.OutboundAIProviderOpenAI)
	anthropic := seedAIIntegration(t, e, "claude", api.OutboundAIProviderAnthropic)
	plain := seedAIIntegration(t, e, "plain", "")

	for name, req := range map[string]api.CreateOutboundAIRouteRequest{
		"mixed providers":  {Name: "mixed", Targets: []api.OutboundAIRouteTarget{{IntegrationID: primary}, {IntegrationID: anthropic}}},
		"not an AI target": {Name: "plain", Targets: []api.OutboundAIRouteTarget{{IntegrationID: plain}}},
		"bad name":         {Name: "Bad_Name", Targets: []api.OutboundAIRouteTarget{{IntegrationID: primary}}},
		"no targets":       {Name: "empty"},
	} {
		resp := e.Do(t, http.MethodPost, "/v1/outbound/ai-routes", req, nil)
		if resp.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d %s", name, resp.Code, resp.Body.String())
		}
	}
	created := e.Do(t, http.MethodPost, "/v1/outbound/ai-routes", api.CreateOutboundAIRouteRequest{Name: "chat", Targets: []api.OutboundAIRouteTarget{
		{IntegrationID: primary}, {IntegrationID: backup, Model: "gpt-4o-mini"},
	}}, nil)
	var route api.OutboundAIRoute
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &route) != nil || len(route.Targets) != 2 || route.Targets[1].Model != "gpt-4o-mini" {
		t.Fatalf("create route = %d %s", created.Code, created.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/outbound/ai-routes", api.CreateOutboundAIRouteRequest{Name: "chat",
		Targets: []api.OutboundAIRouteTarget{{IntegrationID: primary}}}, nil), http.StatusConflict, api.CodeConflict)
	update := e.Do(t, http.MethodPut, "/v1/outbound/ai-routes/"+route.ID+"/targets", api.PutOutboundAIRouteTargetsRequest{
		Targets: []api.OutboundAIRouteTarget{{IntegrationID: backup}},
	}, nil)
	if update.Code != http.StatusNoContent {
		t.Fatalf("update targets = %d %s", update.Code, update.Body.String())
	}
	got := e.Do(t, http.MethodGet, "/v1/outbound/ai-routes/"+route.ID, nil, nil)
	if json.Unmarshal(got.Body.Bytes(), &route) != nil || len(route.Targets) != 1 || route.Targets[0].IntegrationID != backup {
		t.Fatalf("route after update = %s", got.Body.String())
	}
	var routes api.OutboundAIRouteList
	if list := e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil); json.Unmarshal(list.Body.Bytes(), &routes) != nil || len(routes.Items) != 1 {
		t.Fatalf("route list = %s", list.Body.String())
	}
	if del := e.Do(t, http.MethodDelete, "/v1/outbound/ai-routes/"+route.ID, nil, nil); del.Code != http.StatusNoContent {
		t.Fatalf("delete route = %d", del.Code)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/ai-routes/"+route.ID, nil, nil), http.StatusNotFound, api.CodeNotFound)
}
