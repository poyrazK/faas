package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestAuditExpiredCircuitRuleStillCompiles(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	rules := []state.EdgeRule{{ID: "expired-rule", AppID: "app", Enabled: true,
		Kind: state.EdgeRuleKindCircuitBreaker, ExpiresAt: &expired,
		Action: state.EdgeRuleAction{CircuitBreaker: &state.EdgeRuleCircuitBreakerAction{
			FailureThreshold: 0.1, MinRequests: 1, WindowSeconds: 10, OpenSeconds: 3600, MaxOpenSeconds: 3600,
		}},
	}}
	compiled, errs := compileCircuitBreakerRules(rules)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(compiled) != 0 {
		t.Errorf("expired circuit rule remains eligible for live tuning: %+v", compiled)
	}
}

func TestCompiledBodyAndParameterRulesRecoverAfterSchemaReset(t *testing.T) {
	adapter := newEdgeValidateAdapter(nil)
	g := &gatewaydEdgeRules{validate: adapter}
	out, failures := g.compileValidateRules([]state.EdgeRule{{
		ID: "validate", AppID: "app", AccountID: "account", Enabled: true, Kind: state.EdgeRuleKindValidate, MatchPath: "/items",
		Action: state.EdgeRuleAction{Validate: &state.EdgeRuleValidateAction{
			Schema: []byte(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`), RejectOnUnknown: true,
			Parameters: &api.EdgeRuleValidateParameters{Query: []byte(`{"type":"object","properties":{"page":{"type":"integer"}},"required":["page"]}`)},
		}},
	}})
	if len(failures) != 0 || len(out) != 1 {
		t.Fatalf("compile: %+v %+v", out, failures)
	}
	rule := out[0]
	for _, body := range []bool{true, false} {
		adapter.Reset()
		in := &gateway.EdgeValidateIn{Body: []byte(`{"name":"Ada"}`)}
		if !body {
			in.Body = []byte(`{"page":1}`)
			in.Digest = &rule.Parameters.Query.Digest
			in.Schema = rule.Parameters.Query.Schema
		}
		result, err := adapter.Validate(context.Background(), in, &rule)
		if err != nil || !result.OK {
			t.Fatalf("schema reset broke body=%v: %+v %v", body, result, err)
		}
	}
}

func TestServingAndInvalidationHostMatchersAgree(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "host-parity@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "host-parity", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	patterns := []string{"*", "*.example.com", "api?.example.com", "api_.example.com", "api%.example.com", `api\_.example.com`, `api\*.example.com`, "api[12].example.com", "api.example.com"}
	hosts := []string{"example.com", "api.example.com", "api1.example.com", "apié.example.com", "api_.example.com", "api%.example.com", "api-one.example.com", "a.b.example.com", "api[12].example.com"}
	for _, pattern := range patterns {
		rule, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID, MatchHost: pattern, MatchPath: "/**", Enabled: true, Kind: state.EdgeRuleKindIP, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"192.0.2.0/24"}}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range hosts {
			rows, err := mem.OwnedEdgeRulesForHost(ctx, host, acct.ID, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			serving := false
			for _, row := range rows {
				if row.ID == rule.ID {
					serving = true
				}
			}
			if invalidated := api.EdgeRuleHostMatches(pattern, host); invalidated != serving {
				t.Errorf("invalidation=%v serving=%v for pattern=%q host=%q", invalidated, serving, pattern, host)
			}
		}
	}
}
