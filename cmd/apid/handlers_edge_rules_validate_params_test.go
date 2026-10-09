package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func validateParamsRequest(matchPath string, params *api.EdgeRuleValidateParameters) api.CreateEdgeRuleRequest {
	action, _ := json.Marshal(api.EdgeRuleValidateAction{Parameters: params})
	return api.CreateEdgeRuleRequest{
		MatchHost: "api.example.com", MatchPath: matchPath, MatchMethods: []string{"GET"},
		Kind: string(state.EdgeRuleKindValidate), Action: action,
	}
}

func TestCreateEdgeRuleValidateParameters(t *testing.T) {
	e := setup(t, api.PlanHobby)
	slug := mustSeedEdgeRuleApp(t, e, "orders")
	params := &api.EdgeRuleValidateParameters{
		PathTemplate: "/orders/{id}",
		Path:         json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`),
		Query:        json.RawMessage(`{"type":"object","properties":{"expand":{"type":"boolean"}}}`),
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", validateParamsRequest("/orders/?*", params), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created api.EdgeRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	rules, err := e.store.ListEdgeRulesForApp(t.Context(), created.AppID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("stored rules = %d, err=%v", len(rules), err)
	}
	stored := rules[0]
	if stored.Action.Validate == nil || stored.Action.Validate.Parameters == nil ||
		stored.Action.Validate.Parameters.PathTemplate != "/orders/{id}" || len(stored.Action.Validate.Schema) != 0 {
		t.Fatalf("stored action = %+v, err=%v", stored.Action.Validate, err)
	}

	// A path-only update must keep the stored path parameters aligned.
	newPath := "/orders/*"
	rec = e.do(t, http.MethodPatch, "/v1/edge-rules/"+created.ID, api.UpdateEdgeRuleRequest{MatchPath: &newPath}, nil)
	if rec.Code < 400 || !strings.Contains(rec.Body.String(), "match_path must be") {
		t.Fatalf("misaligned path update: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", validateParamsRequest("/orders/*", params), nil)
	if rec.Code < 400 || !strings.Contains(rec.Body.String(), "match_path must be") {
		t.Fatalf("misaligned create: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPostAppOpenAPIPolicyApply_ParameterOnlyOperation(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedApp(t, e, "params-apply")
	doc := `{"openapi":"3.1.0","info":{"title":"p","version":"1"},"paths":{"/orders/{id}":{"get":{
	  "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}},
	                {"name":"expand","in":"query","schema":{"type":"boolean"}}],
	  "responses":{"200":{"description":"OK"}}}}}}`
	seedImport(t, e, app.ID, []byte(doc), 1, "3.1.0")
	planRec := e.do(t, "POST", "/v1/apps/params-apply/openapi/apply", map[string]any{}, nil)
	var plan api.AppOpenAPIPolicyApplyResponse
	if err := json.Unmarshal(planRec.Body.Bytes(), &plan); err != nil || len(plan.Suggestions) != 1 {
		t.Fatalf("plan: %d %s", planRec.Code, planRec.Body.String())
	}
	applyRec := e.do(t, "POST", "/v1/apps/params-apply/openapi/apply", api.ApplyAppOpenAPIPolicyRequest{
		Confirm: true, PreviewSHA256: plan.PreviewSHA256,
	}, nil)
	var applied api.AppOpenAPIPolicyApplyResponse
	if err := json.Unmarshal(applyRec.Body.Bytes(), &applied); err != nil || applied.AppliedCount != 1 {
		t.Fatalf("apply: %d %s", applyRec.Code, applyRec.Body.String())
	}
	if rule := applied.Applied[0]; rule.MatchPath != "/orders/?*" || !strings.Contains(string(rule.Action), "path_template") {
		t.Fatalf("applied rule = %+v action=%s", rule, rule.Action)
	}
}
