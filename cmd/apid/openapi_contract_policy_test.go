package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuildAppOpenAPIContractPolicyDefaultsAndValidates(t *testing.T) {
	s := &server{}
	acct := state.Account{ID: "acct-openapi-policy", Plan: api.PlanPro, Status: state.AccountActive}
	limits := api.MustLimitsFor(acct.Plan)
	app, prob := s.buildApp(acct, api.CreateAppRequest{Slug: "policy-app"}, limits)
	if prob != nil {
		t.Fatal(prob)
	}
	if app.OpenAPIContractPolicy != api.OpenAPIContractPolicyObserve {
		t.Fatalf("default policy = %q, want observe", app.OpenAPIContractPolicy)
	}
	for _, policy := range []string{api.OpenAPIContractPolicyObserve, api.OpenAPIContractPolicyWarn, api.OpenAPIContractPolicyBlock} {
		policy := policy
		app, prob := s.buildApp(acct, api.CreateAppRequest{Slug: "policy-" + policy, OpenAPIContractPolicy: &policy}, limits)
		if prob != nil {
			t.Fatalf("policy %q rejected: %v", policy, prob)
		}
		if app.OpenAPIContractPolicy != policy {
			t.Fatalf("policy %q round trip = %q", policy, app.OpenAPIContractPolicy)
		}
	}
	invalid := "enforce"
	if _, prob := s.buildApp(acct, api.CreateAppRequest{Slug: "policy-invalid", OpenAPIContractPolicy: &invalid}, limits); prob == nil || prob.Code != api.CodeOpenAPIContractPolicyInvalid {
		t.Fatalf("invalid policy problem = %#v, want %s", prob, api.CodeOpenAPIContractPolicyInvalid)
	}
}

func TestValidateUpdateAppOpenAPIContractPolicy(t *testing.T) {
	acct := state.Account{ID: "acct-openapi-policy", Plan: api.PlanPro, Status: state.AccountActive}
	limits := api.MustLimitsFor(acct.Plan)
	app := state.App{AccountID: acct.ID, Slug: "policy-app", MaxConcurrency: 1}
	for _, policy := range []string{api.OpenAPIContractPolicyObserve, api.OpenAPIContractPolicyWarn, api.OpenAPIContractPolicyBlock} {
		policy := policy
		if prob := validateUpdateApp(&api.UpdateAppRequest{OpenAPIContractPolicy: &policy}, acct, limits, app); prob != nil {
			t.Fatalf("policy %q rejected: %v", policy, prob)
		}
	}
	invalid := "enforce"
	if prob := validateUpdateApp(&api.UpdateAppRequest{OpenAPIContractPolicy: &invalid}, acct, limits, app); prob == nil || prob.Code != api.CodeOpenAPIContractPolicyInvalid {
		t.Fatalf("invalid policy problem = %#v, want %s", prob, api.CodeOpenAPIContractPolicyInvalid)
	}
}

func TestUpdateAppOpenAPIContractPolicyRoundTrip(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "policy-roundtrip")
	warn := api.OpenAPIContractPolicyWarn
	rec := e.do(t, "PATCH", "/v1/apps/policy-roundtrip", api.UpdateAppRequest{OpenAPIContractPolicy: &warn}, nil)
	if rec.Code != 200 {
		t.Fatalf("PATCH status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.OpenAPIContractPolicy != warn {
		t.Fatalf("response policy = %q, want %q", out.OpenAPIContractPolicy, warn)
	}
}
