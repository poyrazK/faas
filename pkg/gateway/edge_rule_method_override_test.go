package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// ADR-20261107190000: a gate's match condition reads every path
// form its match_path covers, so "maintenance on everything but /public"
// still covers /public/../admin, which frameworks serve as /admin.
func TestMatchProtectiveEdgeRuleConditionCoversPathForms(t *testing.T) {
	exceptPublic, err := api.CompileEdgeRuleMatch(&api.EdgeRuleMatchExpr{Not: &api.EdgeRuleMatchExpr{Field: "path", Op: "prefix", Value: "/public"}})
	if err != nil {
		t.Fatal(err)
	}
	rule := maintenanceRule("all-but-public", 10)
	rule.PathGlob = "/**"
	rule.Match = exceptPublic
	rules := []gateway.EdgeRuleMaintenanceResolved{rule}
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{"/public/logo.png", false},
		{"/admin/users", true},
		{"/public/../admin/users", true},
		{"/public/%2e%2e/admin/users", true},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://example.test"+tc.target, nil)
		if got := matchMaintenance(r, rules); (got != nil) != tc.want {
			t.Fatalf("%s: match = %#v, want matched=%v", tc.target, got, tc.want)
		}
	}
}

func maintenanceRule(id string, priority int, methods ...string) gateway.EdgeRuleMaintenanceResolved {
	rule := gateway.EdgeRuleMaintenanceResolved{ID: id, AccountID: "acct", Priority: priority, PathGlob: "/admin/**", RetryAfterSeconds: 1}
	if len(methods) > 0 {
		rule.Methods = map[string]bool{}
		for _, m := range methods {
			rule.Methods[m] = true
		}
	}
	return rule
}

func matchMaintenance(r *http.Request, rules []gateway.EdgeRuleMaintenanceResolved) *gateway.EdgeRuleMaintenanceResolved {
	ctx := gateway.WithEdgeRuleMethodOverride(context.Background(), r)
	account := func(rule *gateway.EdgeRuleMaintenanceResolved) string { return rule.AccountID }
	return gateway.MatchProtectiveEdgeRule(ctx, rules, account, r.URL.Path, r.Method, gateway.PickFirstMaintenanceMatch)
}

// ADR-091 amendment 2026-11-02, later: a POST the app runs as DELETE through
// a method-override header or ?_method= meets the DELETE-scoped gate.
func TestMatchProtectiveEdgeRuleCoversMethodOverride(t *testing.T) {
	rules := []gateway.EdgeRuleMaintenanceResolved{maintenanceRule("delete-only", 10, http.MethodDelete)}
	for _, tc := range []struct {
		name, target, header string
		want                 bool
	}{
		{name: "plain POST", target: "/admin/users"},
		{name: "header override", target: "/admin/users", header: "DELETE", want: true},
		{name: "query override", target: "/admin/users?_method=delete", want: true},
		{name: "override to an unscoped method", target: "/admin/users", header: "PATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.target, nil)
			if tc.header != "" {
				r.Header.Set("X-HTTP-Method-Override", tc.header)
			}
			if got := matchMaintenance(r, rules); (got != nil) != tc.want {
				t.Fatalf("match = %#v, want matched=%v", got, tc.want)
			}
		})
	}
}

func TestMatchProtectiveEdgeRulePrefersHigherPriorityAcrossMethods(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/admin/users", nil)
	r.Header.Set("X-HTTP-Method-Override", http.MethodDelete)

	rules := []gateway.EdgeRuleMaintenanceResolved{maintenanceRule("delete", 5, http.MethodDelete), maintenanceRule("post", 10, http.MethodPost)}
	if got := matchMaintenance(r, rules); got == nil || got.ID != "delete" {
		t.Fatalf("override match with higher priority = %#v, want delete", got)
	}
	rules = []gateway.EdgeRuleMaintenanceResolved{maintenanceRule("post", 5, http.MethodPost), maintenanceRule("delete", 10, http.MethodDelete)}
	if got := matchMaintenance(r, rules); got == nil || got.ID != "post" {
		t.Fatalf("request-method match with higher priority = %#v, want post", got)
	}
	rules = []gateway.EdgeRuleMaintenanceResolved{maintenanceRule("delete", 5, http.MethodDelete), maintenanceRule("post", 5, http.MethodPost)}
	if got := matchMaintenance(r, rules); got == nil || got.ID != "post" {
		t.Fatalf("tie = %#v, want the request method's match", got)
	}
}
