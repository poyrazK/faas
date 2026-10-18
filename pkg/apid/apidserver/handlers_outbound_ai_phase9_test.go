package apidserver_test

// adr: 1431 — access tokens and evaluations are served behind the AI
// gateway release gate, on the app and route paths the spec names.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOutboundAIAccessTokensAndEvaluationsRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.CreateApp(t, e, "ai-token-app")
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/ai-access-tokens", nil, nil),
		http.StatusForbidden, "outbound_ai_gateway_unavailable")
	first := seedAIIntegration(t, e, "llm", api.OutboundAIProviderOpenAI)
	second := seedAIIntegration(t, e, "claude", api.OutboundAIProviderAnthropic)
	if bound := e.Do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/outbound-bindings/"+first, nil, nil); bound.Code != http.StatusOK {
		t.Fatalf("bind = %d %s", bound.Code, bound.Body.String())
	}
	tokens := "/v1/apps/" + app.Slug + "/ai-access-tokens"
	created := e.Do(t, http.MethodPost, tokens, api.CreateOutboundAIAccessTokenRequest{Name: "ci", IntegrationIDs: []string{first}}, nil)
	var token api.CreatedOutboundAIAccessToken
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &token) != nil || len(token.Token) != 48 {
		t.Fatalf("create token = %d %s", created.Code, created.Body.String())
	}
	if list := e.Do(t, http.MethodGet, tokens, nil, nil); list.Code != http.StatusOK {
		t.Fatalf("list tokens = %d %s", list.Code, list.Body.String())
	}
	if revoked := e.Do(t, http.MethodDelete, tokens+"/"+token.AccessToken.ID, nil, nil); revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", revoked.Code, revoked.Body.String())
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/apps/missing-app/ai-access-tokens", nil, nil), http.StatusNotFound, api.CodeNotFound)

	route, err := (*e.Store()).CreateOutboundAIRoute(context.Background(), state.OutboundAIRoute{ID: uuid.NewString(), AccountID: (*e.Acct()).ID,
		Name: "chat", Format: api.OutboundAIRouteFormatOpenAI,
		Targets: []state.OutboundAIRouteTarget{{IntegrationID: first}, {IntegrationID: second, Model: "claude-sonnet-4-5"}}})
	if err != nil {
		t.Fatal(err)
	}
	evaluations := "/v1/outbound/ai-routes/" + route.ID + "/evaluations"
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, evaluations, api.CreateOutboundAIEvaluationRequest{Target: 1}, nil),
		http.StatusBadRequest, api.CodeValidation)
	started := e.Do(t, http.MethodPost, evaluations, api.CreateOutboundAIEvaluationRequest{Target: 2, JudgeIntegrationID: first, JudgeModel: "gpt-5"}, nil)
	var evaluation api.OutboundAIEvaluation
	if started.Code != http.StatusAccepted || json.Unmarshal(started.Body.Bytes(), &evaluation) != nil || evaluation.State != api.OutboundAIEvaluationPending {
		t.Fatalf("create evaluation = %d %s", started.Code, started.Body.String())
	}
	if list := e.Do(t, http.MethodGet, evaluations, nil, nil); list.Code != http.StatusOK {
		t.Fatalf("list evaluations = %d %s", list.Code, list.Body.String())
	}
	if got := e.Do(t, http.MethodGet, "/v1/outbound/ai-evaluations/"+evaluation.ID, nil, nil); got.Code != http.StatusOK {
		t.Fatalf("get evaluation = %d %s", got.Code, got.Body.String())
	}
}
