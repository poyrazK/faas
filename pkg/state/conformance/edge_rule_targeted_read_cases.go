package conformance

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testEdgeRuleTargetedReads pins ADR-091 amendment 2026-10-30 (round 5): the
// CORS preset barrier and the app rename read only the rules they change.
// Both stores return the account's rules that use the preset and its route
// rules that send to the slug, in match order, and nothing of another account.
func testEdgeRuleTargetedReads(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ctx := fx.Store, fx.Ctx
	limits := api.MustLimitsFor(api.PlanPro)
	other, err := store.CreateAccount(ctx, "conformance-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateAppIfUnderQuota(ctx, state.App{AccountID: other.ID, Slug: "conformance-" + uuid.NewString(),
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: limits.RAMMB, MaxConcurrency: limits.MaxConcurrency,
		IdleTimeoutS: limits.IdleTimeoutS}, limits)
	if err != nil {
		t.Fatal(err)
	}
	preset, err := store.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: fx.Account.ID, Name: "shared",
		AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	cors := func(priority int, host string, presetID *string) state.CreateEdgeRuleParams {
		return state.CreateEdgeRuleParams{AccountID: fx.Account.ID, AppID: fx.App.ID, MatchHost: host, MatchPath: "/",
			MatchMethods: []string{}, Priority: priority, Enabled: true, Kind: state.EdgeRuleKindCORSA, CorsPresetID: presetID,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: presetID}}}
	}
	route := func(accountID, appID string, priority int, host, target string) state.CreateEdgeRuleParams {
		return state.CreateEdgeRuleParams{AccountID: accountID, AppID: appID, MatchHost: host, MatchPath: "/",
			MatchMethods: []string{}, Priority: priority, Enabled: true, Kind: state.EdgeRuleKindRoute,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: target}}}
	}
	for _, in := range []state.CreateEdgeRuleParams{
		cors(10, "preset.example.com", &preset.ID),
		cors(20, "inline.example.com", nil),
		route(fx.Account.ID, fx.App.ID, 30, "to-api.example.com", "api"),
		route(fx.Account.ID, fx.App.ID, 40, "to-web.example.com", "web"),
		route(other.ID, otherApp.ID, 30, "other-to-api.example.com", "api"),
	} {
		if _, err := store.CreateEdgeRule(ctx, in); err != nil {
			t.Fatalf("seed %s: %v", in.MatchHost, err)
		}
	}
	hosts := func(rules []state.EdgeRule, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, rule := range rules {
			out = append(out, rule.MatchHost)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		got  []string
		want []string
	}{
		{"account rules", hosts(store.ListEdgeRulesForAccount(ctx, fx.Account.ID)),
			[]string{"preset.example.com", "inline.example.com", "to-api.example.com", "to-web.example.com"}},
		{"preset users", hosts(store.ListEdgeRulesUsingCorsPreset(ctx, fx.Account.ID, preset.ID)), []string{"preset.example.com"}},
		{"preset of another account", hosts(store.ListEdgeRulesUsingCorsPreset(ctx, other.ID, preset.ID)), nil},
		{"unparsable preset id", hosts(store.ListEdgeRulesUsingCorsPreset(ctx, fx.Account.ID, "not-a-preset")), nil},
		{"route rules to api", hosts(store.ListRouteRulesTargeting(ctx, fx.Account.ID, "api")), []string{"to-api.example.com"}},
		{"route rules to web", hosts(store.ListRouteRulesTargeting(ctx, fx.Account.ID, "web")), []string{"to-web.example.com"}},
		{"route rules to nothing", hosts(store.ListRouteRulesTargeting(ctx, fx.Account.ID, "missing")), nil},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
