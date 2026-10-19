package apidserver_test

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

func TestOutboundAIPolicyAndUsage(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.AIGateway)
	id := uuid.NewString()
	(*e.Store()).SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{
		ID: id, AccountID: (*e.Acct()).ID, Name: "llm", Origin: "https://api.anthropic.com",
		AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1/messages"},
		Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed",
	})
	policyPath := "/v1/outbound/integrations/" + id + "/ai"
	limit := int64(2_000_000)
	for _, invalid := range []api.PutOutboundAIPolicyRequest{
		{AIProvider: "cohere"},
		{AIProvider: "", DailyTokenLimit: &limit},
		{AIProvider: api.OutboundAIProviderAnthropic, DailyTokenLimit: outboundAITokens(0)},
		{AIProvider: api.OutboundAIProviderAnthropic, DailyTokenLimit: outboundAITokens(api.MaxOutboundAITokensPerDay + 1)},
	} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodPut, policyPath, invalid, nil), http.StatusBadRequest, api.CodeValidation)
	}
	set := e.Do(t, http.MethodPut, policyPath, api.PutOutboundAIPolicyRequest{AIProvider: api.OutboundAIProviderAnthropic, DailyTokenLimit: &limit}, nil)
	if set.Code != http.StatusNoContent || set.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("set AI policy = %d %s", set.Code, set.Body.String())
	}
	var offers api.OutboundIntegrationOfferList
	list := e.Do(t, http.MethodGet, "/v1/outbound/integrations", nil, nil)
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &offers) != nil || len(offers.Items) != 1 ||
		offers.Items[0].AIProvider != api.OutboundAIProviderAnthropic || offers.Items[0].AIDailyTokenLimit == nil || *offers.Items[0].AIDailyTokenLimit != limit {
		t.Fatalf("offer does not show AI settings: %d %s", list.Code, list.Body.String())
	}

	today := time.Now().UTC().Format("2006-01-02")
	(*e.Store()).SeedOutboundAIUsage(id, today,
		state.OutboundAIUsageItem{Caller: "runs", Model: "claude-sonnet-5", Requests: 3, InputTokens: 300, CachedInputTokens: 200, OutputTokens: 90},
		state.OutboundAIUsageItem{Caller: uuid.NewString(), Model: "claude-haiku-5", Requests: 1, ErrorRequests: 1})
	usagePath := "/v1/outbound/integrations/" + id + "/ai-usage"
	got := e.Do(t, http.MethodGet, usagePath, nil, nil)
	var usage api.OutboundAIUsageResponse
	if got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(got.Body.Bytes(), &usage) != nil {
		t.Fatalf("AI usage = %d %s", got.Code, got.Body.String())
	}
	if usage.UsageDate != today || usage.AIProvider != api.OutboundAIProviderAnthropic || usage.DailyTokenCount != 390 ||
		usage.DailyTokenLimit == nil || *usage.DailyTokenLimit != limit || len(usage.Items) != 2 || usage.Items[0].CachedInputTokens != 200 {
		t.Fatalf("AI usage body = %+v", usage)
	}
	if !usage.ResetsAt.After(time.Now()) {
		t.Fatalf("today's usage resets in the past: %s", usage.ResetsAt)
	}
	for _, date := range []string{"yesterday", time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"),
		time.Now().UTC().AddDate(0, 0, -(api.OutboundAIUsageRetentionDays + 1)).Format("2006-01-02")} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodGet, usagePath+"?date="+date, nil, nil), http.StatusBadRequest, api.CodeValidation)
	}

	off := e.Do(t, http.MethodPut, policyPath, api.PutOutboundAIPolicyRequest{}, nil)
	if off.Code != http.StatusNoContent {
		t.Fatalf("turn AI off = %d %s", off.Code, off.Body.String())
	}
	if offer := firstOutboundOffer(t, e); offer.AIProvider != "" || offer.AIDailyTokenLimit != nil {
		t.Fatalf("AI settings were not cleared: %+v", offer)
	}

	other, err := (*e.Store()).CreateAccount(context.Background(), "other-ai@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherID := uuid.NewString()
	(*e.Store()).SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{
		ID: otherID, AccountID: other.ID, Name: "foreign-llm", Origin: "https://api.openai.com",
		AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"},
		Enabled: true, OwnerKind: "customer", CredentialSource: "customer_sealed",
	})
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, "/v1/outbound/integrations/"+otherID+"/ai",
		api.PutOutboundAIPolicyRequest{AIProvider: api.OutboundAIProviderOpenAI}, nil), http.StatusNotFound, api.CodeNotFound)
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+otherID+"/ai-usage", nil, nil), http.StatusNotFound, api.CodeNotFound)
}

func TestCreateOutboundIntegrationRejectsInvalidAIPolicy(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.AIGateway)
	limit := int64(1000)
	for _, req := range []api.CreateOutboundIntegrationRequest{
		{Name: "llm", Origin: "https://api.openai.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, AIProvider: "cohere"},
		{Name: "llm", Origin: "https://api.openai.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"}, AIDailyTokenLimit: &limit},
	} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/outbound/integrations", req, nil), http.StatusBadRequest, api.CodeValidation)
	}
}

func outboundAITokens(value int64) *int64 { return &value }

func firstOutboundOffer(t *testing.T, e apidserver.TestEnv) api.OutboundIntegrationOffer {
	t.Helper()
	var offers api.OutboundIntegrationOfferList
	list := e.Do(t, http.MethodGet, "/v1/outbound/integrations", nil, nil)
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &offers) != nil || len(offers.Items) == 0 {
		t.Fatalf("list offers = %d %s", list.Code, list.Body.String())
	}
	return offers.Items[0]
}
