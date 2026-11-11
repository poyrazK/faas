package edgerules

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-31 (security review): the gateway applies an
// account's rules to every host they cover, so a deploy token, which deploys
// one app, may write rules only for that app's own hosts.
func TestAppBoundCredentialRulesStayOnTheirApp(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "app-bound@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) state.App {
		t.Helper()
		a, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	shop, blog := newApp("shop"), newApp("blog")
	if _, err := mem.CreateCustomDomain(ctx, "shop.example.com", shop.ID, "token-shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.CreateCustomDomain(ctx, "blog.example.com", blog.ID, "token-blog"); err != nil {
		t.Fatal(err)
	}
	apps := hostidentity.Configured().Apps
	svc := &Service{Host: attackModeHost{store: mem, app: shop, audits: &[]string{}, notif: &recordingNotifier{}}}
	deployToken := authmw.WithPrincipal(ctx, acct, &state.APIKey{ID: "dt-1", AccountID: acct.ID, AppID: shop.ID, Scopes: []string{api.ScopeDeployWrite}}, nil)
	otherAppToken := authmw.WithPrincipal(ctx, acct, &state.APIKey{ID: "dt-2", AccountID: acct.ID, AppID: blog.ID, Scopes: []string{api.ScopeDeployWrite}}, nil)
	accountKey := authmw.WithPrincipal(ctx, acct, &state.APIKey{ID: "key-1", AccountID: acct.ID, Scopes: []string{api.ScopeDeployWrite}}, nil)
	session := authmw.WithPrincipal(ctx, acct, nil, nil)

	for _, tc := range []struct {
		name string
		ctx  context.Context
		host string
		ok   bool
	}{
		{"token: every host of its app", deployToken, "@app", true},
		{"token: its app's own host", deployToken, "shop." + apps, true},
		{"token: its own host in another case", deployToken, "SHOP." + apps, true},
		{"token: its custom domain", deployToken, "shop.example.com", true},
		{"token: every host", deployToken, "*", false},
		{"token: every app under the apps domain", deployToken, "*." + apps, false},
		{"token: another app's host", deployToken, "blog." + apps, false},
		{"token: another app's custom domain", deployToken, "blog.example.com", false},
		{"token: a host nobody owns", deployToken, "unrelated.example.org", false},
		{"token bound to another app", otherAppToken, "@app", false},
		{"account key: every host", accountKey, "*", true},
		{"session: every host", session, "*", true},
		{"no principal", ctx, "*", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prob := svc.AppBoundHostProblem(tc.ctx, shop, tc.host)
			if (prob == nil) != tc.ok {
				t.Fatalf("AppBoundHostProblem(%q) = %v, want ok=%v", tc.host, prob, tc.ok)
			}
			if prob != nil && prob.Status != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", prob.Status)
			}
		})
	}

	// The create path refuses before anything is stored, and still creates
	// a rule on the token's own app.
	deny := json.RawMessage(`{"deny":["203.0.113.0/24"]}`)
	if _, prob := svc.CreateRule(deployToken, acct, shop, api.CreateEdgeRuleRequest{MatchHost: "*", MatchPath: "/", Kind: "ip", Action: deny}); prob == nil || prob.Status != http.StatusForbidden {
		t.Fatalf("deploy token created a rule on every host: %v", prob)
	}
	if rules, _ := mem.ListEdgeRulesForApp(ctx, shop.ID); len(rules) != 0 {
		t.Fatalf("refused create stored %d rules", len(rules))
	}
	created, prob := svc.CreateRule(deployToken, acct, shop, api.CreateEdgeRuleRequest{MatchHost: "@app", MatchPath: "/", Kind: "ip", Action: deny})
	if prob != nil {
		t.Fatalf("deploy token could not create a rule on its own app: %v", prob)
	}
	wider := "*"
	if _, prob := svc.UpdateRule(deployToken, acct, created.ID, api.UpdateEdgeRuleRequest{MatchHost: &wider}); prob == nil || prob.Status != http.StatusForbidden {
		t.Fatalf("deploy token widened its rule to every host: %v", prob)
	}
}
