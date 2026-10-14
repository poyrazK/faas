package apidserver_test

// adr: 991 — guardrails, key pools, alerts, request log and route formats.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOutboundBindingAIGuardrails(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.CreateApp(t, e, "guarded-app")
	id := seedAIIntegration(t, e, "llm", api.OutboundAIProviderGemini)
	bindingPath := "/v1/apps/" + app.Slug + "/outbound-bindings/" + id
	if bound := e.Do(t, http.MethodPut, bindingPath, nil, nil); bound.Code != http.StatusOK {
		t.Fatalf("bind = %d %s", bound.Code, bound.Body.String())
	}
	for name, req := range map[string]api.PutOutboundBindingAIGuardrailsRequest{
		"zero rpm":      {RequestsPerMinute: outboundAITokens(0)},
		"empty list":    {AllowedModels: []string{}},
		"inner star":    {AllowedModels: []string{"gpt-*-mini"}},
		"huge ceiling":  {MaxOutputTokens: outboundAITokens(2_000_000)},
		"duplicate":     {AllowedModels: []string{"a", "a"}},
		"lonely star":   {AllowedModels: []string{"*"}},
		"tpm too large": {TokensPerMinute: outboundAITokens(api.MaxOutboundAITokensPerMinute + 1)},
	} {
		apidserver.AssertProblem(t, e.Do(t, http.MethodPut, bindingPath+"/ai-guardrails", req, nil), http.StatusBadRequest, api.CodeValidation)
		_ = name
	}
	set := e.Do(t, http.MethodPut, bindingPath+"/ai-guardrails", api.PutOutboundBindingAIGuardrailsRequest{
		RequestsPerMinute: outboundAITokens(60), TokensPerMinute: outboundAITokens(100_000),
		AllowedModels: []string{"gemini-2.5-flash", "gemini-2.5-*"}, MaxOutputTokens: outboundAITokens(2048),
	}, nil)
	if set.Code != http.StatusNoContent {
		t.Fatalf("set guardrails = %d %s", set.Code, set.Body.String())
	}
	var bindings api.OutboundAppBindingList
	list := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/outbound-bindings", nil, nil)
	if json.Unmarshal(list.Body.Bytes(), &bindings) != nil || len(bindings.Items) != 1 {
		t.Fatalf("bindings = %s", list.Body.String())
	}
	got := bindings.Items[0]
	if got.AIRequestsPerMinute == nil || *got.AIRequestsPerMinute != 60 || got.AITokensPerMinute == nil || len(got.AIAllowedModels) != 2 ||
		got.AIMaxOutputTokens == nil || *got.AIMaxOutputTokens != 2048 {
		t.Fatalf("binding guardrails = %+v", got)
	}
	plain := seedAIIntegration(t, e, "plain", "")
	if bound := e.Do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/outbound-bindings/"+plain, nil, nil); bound.Code != http.StatusOK {
		t.Fatalf("bind plain = %d", bound.Code)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/outbound-bindings/"+plain+"/ai-guardrails",
		api.PutOutboundBindingAIGuardrailsRequest{RequestsPerMinute: outboundAITokens(5)}, nil), http.StatusNotFound, api.CodeNotFound)
}

func TestOutboundCredentialKeyPool(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previous := (*apidserver.OutboundCredentialRecipient)
	(*apidserver.OutboundCredentialRecipient) = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { (*apidserver.OutboundCredentialRecipient) = previous })
	id := seedAIIntegration(t, e, "llm", api.OutboundAIProviderOpenAI)
	keysPath := "/v1/outbound/integrations/" + id + "/credential-keys"
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, keysPath, api.CreateOutboundCredentialKeyRequest{Label: "Bad Label", Authorization: "Bearer x"}, nil),
		http.StatusBadRequest, api.CodeValidation)
	created := e.Do(t, http.MethodPost, keysPath, api.CreateOutboundCredentialKeyRequest{Label: "second", Authorization: "Bearer sk-second"}, nil)
	var key api.OutboundCredentialKey
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &key) != nil || key.Label != "second" || key.ID == "" {
		t.Fatalf("create key = %d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), "sk-second") {
		t.Fatal("the key value was returned")
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, keysPath, api.CreateOutboundCredentialKeyRequest{Label: "second", Authorization: "Bearer y"}, nil),
		http.StatusConflict, api.CodeConflict)
	for i := 1; i < api.OutboundIntegrationCredentialKeysMax; i++ {
		label := "key-" + string(rune('a'+i))
		if resp := e.Do(t, http.MethodPost, keysPath, api.CreateOutboundCredentialKeyRequest{Label: label, Authorization: "Bearer k"}, nil); resp.Code != http.StatusCreated {
			t.Fatalf("key %d = %d", i, resp.Code)
		}
	}
	if over := e.Do(t, http.MethodPost, keysPath, api.CreateOutboundCredentialKeyRequest{Label: "one-more", Authorization: "Bearer k"}, nil); over.Code != http.StatusTooManyRequests {
		t.Fatalf("key over the limit = %d", over.Code)
	}
	var keys api.OutboundCredentialKeyList
	if list := e.Do(t, http.MethodGet, keysPath, nil, nil); json.Unmarshal(list.Body.Bytes(), &keys) != nil || len(keys.Items) != api.OutboundIntegrationCredentialKeysMax {
		t.Fatalf("key list = %s", list.Body.String())
	}
	if del := e.Do(t, http.MethodDelete, keysPath+"/"+key.ID, nil, nil); del.Code != http.StatusNoContent {
		t.Fatalf("delete key = %d", del.Code)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodDelete, keysPath+"/"+key.ID, nil, nil), http.StatusNotFound, api.CodeNotFound)
}

func TestOutboundAIAlertsAndRequestLog(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := seedAIIntegration(t, e, "llm", api.OutboundAIProviderOpenAI)
	logOn := e.Do(t, http.MethodPut, "/v1/outbound/integrations/"+id+"/ai", api.PutOutboundAIPolicyRequest{
		AIProvider: api.OutboundAIProviderOpenAI, RequestLogEnabled: true,
	}, nil)
	if logOn.Code != http.StatusNoContent || !firstOutboundOffer(t, e).AIRequestLogEnabled {
		t.Fatalf("request log policy = %d %s", logOn.Code, logOn.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, "/v1/outbound/integrations/"+id+"/ai", api.PutOutboundAIPolicyRequest{RequestLogEnabled: true}, nil),
		http.StatusBadRequest, api.CodeValidation)

	day := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	(*e.Store()).SeedOutboundAIBudgetAlert(state.OutboundAIBudgetAlert{ID: "a1", IntegrationID: id, Budget: api.OutboundAIBudgetDailyCost,
		PeriodStart: day, ThresholdPercent: 80, Limit: 1000, Used: 820, CrossedAt: day.Add(time.Hour)})
	(*e.Store()).SeedOutboundAIBudgetAlert(state.OutboundAIBudgetAlert{ID: "a2", IntegrationID: id, Budget: api.OutboundAIBudgetAppDailyTokens,
		AppID: "app-1", PeriodStart: day, ThresholdPercent: 100, Limit: 50, Used: 60, CrossedAt: day.Add(2 * time.Hour)})
	var alerts api.OutboundAIBudgetAlertList
	got := e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-alerts", nil, nil)
	if json.Unmarshal(got.Body.Bytes(), &alerts) != nil || len(alerts.Items) != 2 || alerts.Items[0].ID != "a2" ||
		alerts.Items[0].AppID == nil || alerts.Items[1].AppID != nil || alerts.Items[1].PeriodStart != "2026-10-11" {
		t.Fatalf("alerts = %s", got.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-alerts?limit=0", nil, nil), http.StatusBadRequest, api.CodeValidation)

	ttfb := int64(120)
	for i := 0; i < 3; i++ {
		(*e.Store()).SeedOutboundAIRequestLog(id, state.OutboundAIRequestLogEntry{ID: "r" + string(rune('0'+i)), CreatedAt: day.Add(time.Duration(i) * time.Minute),
			Caller: "runs", Model: "gpt-4o", Status: 200, Outcome: "known", InputTokens: 10, OutputTokens: 5, DurationMS: 900, TTFBMS: &ttfb, Attempts: 1})
	}
	var page api.OutboundAIRequestLogList
	first := e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-requests?limit=2", nil, nil)
	if json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Items) != 2 || page.Items[0].ID != "r2" || page.NextBefore == nil ||
		page.Items[0].TimeToFirstByteMS == nil || *page.Items[0].TimeToFirstByteMS != 120 {
		t.Fatalf("first page = %s", first.Body.String())
	}
	second := e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-requests?limit=2&before="+page.NextBefore.Format(time.RFC3339Nano), nil, nil)
	if json.Unmarshal(second.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != "r0" || page.NextBefore != nil {
		t.Fatalf("second page = %s", second.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/integrations/"+id+"/ai-requests?before=yesterday", nil, nil), http.StatusBadRequest, api.CodeValidation)
}

func TestAIGatewayReleaseGateIsClosedByDefault(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil), http.StatusForbidden, "outbound_ai_gateway_unavailable")
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/outbound/integrations", api.CreateOutboundIntegrationRequest{
		Name: "llm", Origin: "https://api.openai.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"},
		AIProvider: api.OutboundAIProviderOpenAI,
	}, nil), http.StatusForbidden, "outbound_ai_gateway_unavailable")
	// Ordinary integrations are not refused by the gate. The origin check
	// that follows performs a real DNS lookup, so only the gate is asserted.
	if plain := e.Do(t, http.MethodPost, "/v1/outbound/integrations", api.CreateOutboundIntegrationRequest{
		Name: "plain", Origin: "https://api.example.com", AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1"},
	}, nil); strings.Contains(plain.Body.String(), "outbound_ai_gateway_unavailable") {
		t.Fatalf("the gate refused a plain integration: %d %s", plain.Code, plain.Body.String())
	}
	for _, path := range []string{"/ai", "/ai-usage", "/ai-prices", "/ai-alerts", "/ai-requests", "/credential-keys"} {
		method := http.MethodGet
		if path == "/ai" {
			method = http.MethodPut
		}
		apidserver.AssertProblem(t, e.Do(t, method, "/v1/outbound/integrations/"+uuid.NewString()+path, api.PutOutboundAIPolicyRequest{}, nil),
			http.StatusForbidden, "outbound_ai_gateway_unavailable")
	}
	if err := (*e.S()).ConfigureAIGateway(apidserver.Config{}, func(name string) string {
		if name == "FAAS_AI_GATEWAY_ENABLED" {
			return "1"
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	if list := e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil); list.Code != http.StatusOK {
		t.Fatalf("routes after enabling = %d %s", list.Code, list.Body.String())
	}
	if err := (*e.S()).ConfigureAIGateway(apidserver.Config{AIGatewayEnabled: true}, func(name string) string {
		if name == "FAAS_AI_GATEWAY_ENABLED" {
			return "0"
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	if *(*e.S()).AiGatewayEnabled() {
		t.Fatal("FAAS_AI_GATEWAY_ENABLED=0 did not override the TOML setting")
	}
}

func TestOutboundAIRouteFormats(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	openai := seedAIIntegration(t, e, "gpt", api.OutboundAIProviderOpenAI)
	claude := seedAIIntegration(t, e, "claude", api.OutboundAIProviderAnthropic)
	bedrock := seedAIIntegration(t, e, "bedrock", api.OutboundAIProviderBedrock)
	vertex := seedAIIntegration(t, e, "vertex", api.OutboundAIProviderVertex)
	for name, req := range map[string]api.CreateOutboundAIRouteRequest{
		"unknown format":      {Name: "x", Format: "grpc", Targets: []api.OutboundAIRouteTarget{{IntegrationID: openai}}},
		"claude needs model":  {Name: "y", Format: api.OutboundAIRouteFormatOpenAI, Targets: []api.OutboundAIRouteTarget{{IntegrationID: openai}, {IntegrationID: claude}}},
		"bedrock needs model": {Name: "z", Format: api.OutboundAIRouteFormatOpenAI, Targets: []api.OutboundAIRouteTarget{{IntegrationID: bedrock}}},
		// ADR-995: a Vertex target needs one project and location in its prefixes.
		"vertex without location": {Name: "v", Format: api.OutboundAIRouteFormatOpenAI,
			Targets: []api.OutboundAIRouteTarget{{IntegrationID: vertex, Model: "gemini-2.5-pro"}}},
	} {
		if resp := e.Do(t, http.MethodPost, "/v1/outbound/ai-routes", req, nil); resp.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, resp.Code, resp.Body.String())
		}
	}
	// ADR-995: openai routes reach Bedrock through Converse.
	if resp := e.Do(t, http.MethodPost, "/v1/outbound/ai-routes", api.CreateOutboundAIRouteRequest{Name: "bedrock-chat", Format: api.OutboundAIRouteFormatOpenAI,
		Targets: []api.OutboundAIRouteTarget{{IntegrationID: openai}, {IntegrationID: bedrock, Model: "amazon.nova-pro-v1:0"}}}, nil); resp.Code != http.StatusCreated {
		t.Fatalf("bedrock openai route = %d %s", resp.Code, resp.Body.String())
	}
	created := e.Do(t, http.MethodPost, "/v1/outbound/ai-routes", api.CreateOutboundAIRouteRequest{Name: "any-chat", Format: api.OutboundAIRouteFormatOpenAI,
		Targets: []api.OutboundAIRouteTarget{{IntegrationID: openai}, {IntegrationID: claude, Model: "claude-sonnet-4"}}}, nil)
	var route api.OutboundAIRoute
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &route) != nil || route.Format != api.OutboundAIRouteFormatOpenAI {
		t.Fatalf("openai route = %d %s", created.Code, created.Body.String())
	}
	// Changing targets keeps the route's format rules.
	apidserver.AssertProblem(t, e.Do(t, http.MethodPut, "/v1/outbound/ai-routes/"+route.ID+"/targets", api.PutOutboundAIRouteTargetsRequest{
		Targets: []api.OutboundAIRouteTarget{{IntegrationID: claude}},
	}, nil), http.StatusBadRequest, api.CodeValidation)
	native := e.Do(t, http.MethodPost, "/v1/outbound/ai-routes", api.CreateOutboundAIRouteRequest{Name: "plain",
		Targets: []api.OutboundAIRouteTarget{{IntegrationID: openai}}}, nil)
	if json.Unmarshal(native.Body.Bytes(), &route) != nil || route.Format != api.OutboundAIRouteFormatNative {
		t.Fatalf("default format = %s", native.Body.String())
	}
}
