package apidserver_test

// adr: 998 — the AI gateway preview opens per account: the platform gate,
// then the staged rollout, then the plan entitlement, and /v1/capabilities
// reports the same answer.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func aiGatewayCapability(t *testing.T, e apidserver.TestEnv) api.CapabilityStatus {
	t.Helper()
	var response api.CapabilitiesResponse
	got := e.Do(t, http.MethodGet, "/v1/capabilities", nil, nil)
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &response) != nil {
		t.Fatalf("capabilities = %d %s", got.Code, got.Body.String())
	}
	for _, capability := range response.Capabilities {
		if capability.Key == "ai-gateway" {
			return capability
		}
	}
	t.Fatal("capabilities do not list ai-gateway")
	return api.CapabilityStatus{}
}

func TestAIGatewayRolloutAndPlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	if err := (*e.S()).ConfigureAIGateway(apidserver.Config{}, env(map[string]string{
		"FAAS_AI_GATEWAY_ENABLED": "1", "FAAS_AI_GATEWAY_ACCOUNTS": uuid.NewString(),
	})); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil), http.StatusForbidden, "outbound_ai_gateway_unavailable")
	if capability := aiGatewayCapability(t, e); capability.Enabled || capability.UnavailableReason != api.CapabilityUnavailableRuntime {
		t.Fatalf("capability outside the rollout = %+v", capability)
	}
	if err := (*e.S()).ConfigureAIGateway(apidserver.Config{AIGatewayEnabled: true, AIGatewayAccounts: []string{(*e.Acct()).ID}},
		env(nil)); err != nil {
		t.Fatal(err)
	}
	if list := e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil); list.Code != http.StatusOK {
		t.Fatalf("routes inside the rollout = %d %s", list.Code, list.Body.String())
	}
	if capability := aiGatewayCapability(t, e); !capability.Enabled || capability.Maturity != api.CapabilityMaturityPreview {
		t.Fatalf("capability inside the rollout = %+v", capability)
	}
	if err := (*e.S()).ConfigureAIGateway(apidserver.Config{AIGatewayAccounts: []string{"acme"}}, env(nil)); err == nil {
		t.Fatal("a rollout entry that is not a UUID was accepted")
	}

	free := apidserver.Setup(t, api.PlanFree)
	if err := (*free.S()).ConfigureAIGateway(apidserver.Config{AIGatewayEnabled: true}, env(nil)); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, free.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil), http.StatusPaymentRequired, api.CodePlanFeatureGated)
	apidserver.AssertProblem(t, free.Do(t, http.MethodPost, "/v1/outbound/integrations", api.CreateOutboundIntegrationRequest{
		Name: "llm", Origin: "https://api.openai.com", AllowedMethods: []string{"POST"}, AllowedPathPrefixes: []string{"/v1"},
		AIProvider: api.OutboundAIProviderOpenAI,
	}, nil), http.StatusPaymentRequired, api.CodePlanFeatureGated)
	if capability := aiGatewayCapability(t, free); capability.Enabled || capability.UnavailableReason != api.CapabilityUnavailablePlan {
		t.Fatalf("capability on Free = %+v", capability)
	}
}
