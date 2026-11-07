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

// adr: 091 — amendment 2026-11-07: an agent or mcp-login key limited to
// projects (ADR-20261104085712) is held to its app's hosts and its projects'
// route targets, as a deploy token is to its app. The gateway applies an
// owner's "*", glob and other-host rules to every app of that owner, so a
// limited key could otherwise redirect, route, capture or ungate apps
// outside its projects.
func TestProjectLimitedKeyRulesStayInsideItsProjects(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "project-limited@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newProject := func(slug string) string {
		t.Helper()
		p, err := mem.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	inside, outside := newProject("storefront"), newProject("finance")
	newApp := func(slug, project string) state.App {
		t.Helper()
		a, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, ProjectID: project})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	shop, api2, billing := newApp("shop", inside), newApp("shop-api", inside), newApp("billing", outside)
	if _, err := mem.CreateCustomDomain(ctx, "billing.example.com", billing.ID, "token-billing"); err != nil {
		t.Fatal(err)
	}
	apps := hostidentity.Configured().Apps
	svc := &Service{Host: attackModeHost{store: mem, app: shop, audits: &[]string{}, notif: &recordingNotifier{}}}
	limited := authmw.WithPrincipal(ctx, acct, &state.APIKey{ID: "agent-1", AccountID: acct.ID, Scopes: []string{api.ScopeAgent, api.ScopeDeployWrite},
		ProjectLimited: true, ProjectIDs: []string{inside}}, nil)
	accountKey := authmw.WithPrincipal(ctx, acct, &state.APIKey{ID: "key-1", AccountID: acct.ID, Scopes: []string{api.ScopeDeployWrite}}, nil)

	t.Run("match_host", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			app  state.App
			host string
			ok   bool
		}{
			{"every host of its app", shop, "@app", true},
			{"its app's own host", shop, "shop." + apps, true},
			{"every host", shop, "*", false},
			{"every app under the apps domain", shop, "*." + apps, false},
			{"an app in another project", shop, "billing." + apps, false},
			{"another project's custom domain", shop, "billing.example.com", false},
			{"a sibling app in its project", shop, "shop-api." + apps, false},
			{"an app outside its projects", billing, "@app", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				prob := svc.AppBoundHostProblem(limited, tc.app, tc.host)
				if (prob == nil) != tc.ok {
					t.Fatalf("AppBoundHostProblem(%s, %q) = %v, want ok=%v", tc.app.Slug, tc.host, prob, tc.ok)
				}
				if prob != nil && prob.Status != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", prob.Status)
				}
			})
		}
		if prob := svc.AppBoundHostProblem(accountKey, shop, "*"); prob != nil {
			t.Fatalf("an account key was limited: %v", prob)
		}
	})

	t.Run("route target", func(t *testing.T) {
		route := func(slug string) json.RawMessage { return json.RawMessage(`{"target_app_slug":"` + slug + `"}`) }
		if prob := svc.RouteTargetProblem(limited, acct.ID, shop.ID, "route", route(api2.Slug)); prob != nil {
			t.Fatalf("route to an app in its project refused: %v", prob)
		}
		if prob := svc.RouteTargetProblem(limited, acct.ID, shop.ID, "route", route(billing.Slug)); prob == nil || prob.Status != http.StatusForbidden {
			t.Fatalf("route to an app outside its projects = %v, want 403", prob)
		}
		if prob := svc.RouteTargetProblem(limited, acct.ID, shop.ID, "route", route("no-such-app")); prob == nil || prob.Status != http.StatusForbidden {
			t.Fatalf("route to an unknown slug = %v, want the same 403", prob)
		}
		if prob := svc.RouteTargetProblem(accountKey, acct.ID, shop.ID, "route", route(billing.Slug)); prob != nil {
			t.Fatalf("an account key was limited: %v", prob)
		}
	})

	t.Run("stored rules that reach past the app", func(t *testing.T) {
		deny := json.RawMessage(`{"deny":["203.0.113.0/24"]}`)
		wide, prob := svc.CreateRule(accountKey, acct, shop, api.CreateEdgeRuleRequest{MatchHost: "*", MatchPath: "/", Kind: "ip", Action: deny})
		if prob != nil {
			t.Fatalf("account key create: %v", prob)
		}
		disabled := false
		if _, prob := svc.UpdateRule(limited, acct, wide.ID, api.UpdateEdgeRuleRequest{Enabled: &disabled}); prob == nil || prob.Status != http.StatusForbidden {
			t.Fatalf("limited key disabled an every-host rule: %v", prob)
		}
		row, err := mem.GetEdgeRuleByID(ctx, wide.ID)
		if err != nil {
			t.Fatal(err)
		}
		if prob := svc.StoredRuleReachProblem(limited, row); prob == nil {
			t.Fatal("limited key may delete an every-host rule")
		}
		if prob := svc.restoreProblem(limited, acct, shop, nil, []state.EdgeRule{row}); prob == nil || prob.Status != http.StatusForbidden {
			t.Fatalf("limited key rolled back over an every-host rule: %v", prob)
		}
		own, prob := svc.CreateRule(limited, acct, shop, api.CreateEdgeRuleRequest{MatchHost: "@app", MatchPath: "/", Kind: "ip", Action: deny})
		if prob != nil {
			t.Fatalf("limited key could not create a rule on its own app: %v", prob)
		}
		if _, prob := svc.UpdateRule(limited, acct, own.ID, api.UpdateEdgeRuleRequest{Enabled: &disabled}); prob != nil {
			t.Fatalf("limited key could not change its own app's rule: %v", prob)
		}
	})
}
