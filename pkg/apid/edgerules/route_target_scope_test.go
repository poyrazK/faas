package edgerules

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-31 (round 6): a company app keeps its
// creator's account, so a route rule on the creator's personal app could name
// a company app (or the reverse) and pass the same-account check. Such a
// target is refused when the rule is written; the gateway refuses it too.
func TestRouteTargetMustShareTheRuleAppsScope(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "route-scope@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := mem.CreateOrg(ctx, state.Org{Slug: "acme-scope", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug, orgID string) state.App {
		t.Helper()
		a, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	personal, personal2 := newApp("alice-own", ""), newApp("alice-blog", "")
	company, company2 := newApp("acme-api", org.ID), newApp("acme-web", org.ID)
	svc := &Service{Host: attackModeHost{store: mem, audits: &[]string{}}}
	route := func(target string) json.RawMessage {
		raw, _ := json.Marshal(api.EdgeRuleRouteAction{TargetAppSlug: target})
		return raw
	}
	for _, tc := range []struct {
		name   string
		source state.App
		target string
		ok     bool
	}{
		{"personal to personal", personal, personal2.Slug, true},
		{"company to company", company, company2.Slug, true},
		{"personal to company", personal, company.Slug, false},
		{"company to personal", company, personal.Slug, false},
		{"unknown slug reveals nothing", personal, "no-such-app", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prob := svc.RouteTargetProblem(ctx, acct.ID, tc.source.ID, string(state.EdgeRuleKindRoute), route(tc.target))
			if (prob == nil) != tc.ok {
				t.Fatalf("RouteTargetProblem = %v, want ok=%v", prob, tc.ok)
			}
		})
	}
}
