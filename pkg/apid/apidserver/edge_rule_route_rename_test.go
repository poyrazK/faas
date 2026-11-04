package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-30: a route rule names its target app by slug.
// Renaming the target used to leave the rule naming a slug that no longer
// resolved, and the gateway quietly served the routed paths from the host's
// own app; a new app later given the old slug would have received them. The
// rename now points the account's route rules at the new slug.
func TestRenameAppRetargetsRouteRules(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	ctx := t.Context()
	front := apidserver.MustSeedApp(t, e, "shop-front")
	target := apidserver.MustSeedApp(t, e, "shop-api")
	route, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: front,
		MatchHost: "shop.example.com", MatchPath: "/api/**", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindRoute,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: "shop-api"}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: front,
		MatchHost: "shop.example.com", MatchPath: "/admin/**", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindRoute,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: "shop-front"}}})
	if err != nil {
		t.Fatal(err)
	}

	rec := e.Do(t, http.MethodPost, "/v1/apps/shop-api/rename", api.RenameAppRequest{NewSlug: "shop-backend"}, map[string]string{"Idempotency-Key": "rename"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d: %s", rec.Code, rec.Body)
	}
	got, err := store.GetEdgeRuleByID(ctx, route.ID)
	if err != nil || got.Action.Route == nil || got.Action.Route.TargetAppSlug != "shop-backend" {
		t.Fatalf("route rule after the rename = %+v, %v; want it to target shop-backend", got.Action.Route, err)
	}
	if renamed, err := store.AppByID(ctx, target); err != nil || renamed.Slug != "shop-backend" {
		t.Fatalf("renamed app = %q, %v", renamed.Slug, err)
	}
	unchanged, err := store.GetEdgeRuleByID(ctx, other.ID)
	if err != nil || unchanged.Action.Route.TargetAppSlug != "shop-front" {
		t.Fatalf("a route rule to another app changed: %+v, %v", unchanged.Action.Route, err)
	}
}
