package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func wafEdgeRuleRequest(action string) api.CreateEdgeRuleRequest {
	return api.CreateEdgeRuleRequest{
		MatchHost: "api.example.com",
		MatchPath: "/api/*",
		Kind:      string(state.EdgeRuleKindWAF),
		Action:    json.RawMessage(action),
	}
}

func TestCreateEdgeRuleWAFStoresEffectiveDefaults(t *testing.T) {
	e := setup(t, api.PlanPro)
	slug := mustSeedEdgeRuleApp(t, e, "waf-defaults")
	rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", wafEdgeRuleRequest(`{"exclude_rule_ids":[942100,920350]}`), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var response api.EdgeRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := `{"kind":"waf","waf":{"mode":"observe","paranoia_level":1,"anomaly_threshold":5,"exclude_rule_ids":[920350,942100]}}`
	if string(response.Action) != want {
		t.Errorf("action = %s, want %s", response.Action, want)
	}
}

func TestCreateEdgeRuleWAFRejections(t *testing.T) {
	for _, tc := range []struct {
		name     string
		plan     api.Plan
		action   string
		wantCode string
	}{
		{name: "free plan", plan: api.PlanFree, action: `{}`, wantCode: api.CodePlanEdgeRuleKindQuotaReached},
		{name: "hobby plan", plan: api.PlanHobby, action: `{}`, wantCode: api.CodePlanEdgeRuleKindQuotaReached},
		{name: "block mode", plan: api.PlanPro, action: `{"mode":"block"}`, wantCode: api.CodeValidation},
		{name: "paranoia level 3", plan: api.PlanPro, action: `{"paranoia_level":3}`, wantCode: api.CodeValidation},
		{name: "non-CRS exclusion", plan: api.PlanPro, action: `{"exclude_rule_ids":[42]}`, wantCode: api.CodeValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, tc.plan)
			slug := mustSeedEdgeRuleApp(t, e, "waf-reject")
			rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", wafEdgeRuleRequest(tc.action), nil)
			if rec.Code == http.StatusCreated || !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("status = %d, body = %s; want code %s", rec.Code, rec.Body.String(), tc.wantCode)
			}
		})
	}
}
