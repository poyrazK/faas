package state_test

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-30 (round 5): the CORS preset barrier and the
// app rename read only the rules they change. Both stores return the
// account's rules that use the preset, and its route rules that send to the
// slug, and nothing of another account.
func TestEdgeRuleTargetedReads(t *testing.T) {
	t.Run("mem", func(t *testing.T) {
		exerciseEdgeRuleTargetedReads(t, t.Context(), state.NewMemStore())
	})
	t.Run("pg", func(t *testing.T) {
		s, ctx := pgStore(t)
		exerciseEdgeRuleTargetedReads(t, ctx, s)
	})
}

type edgeRuleTargetedReadsStore interface {
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateApp(ctx context.Context, a state.App) (state.App, error)
	CreateCorsPresetIfUnderQuota(ctx context.Context, p state.CorsPreset, limits api.Limits) (state.CorsPreset, error)
	CreateEdgeRule(ctx context.Context, in state.CreateEdgeRuleParams) (state.EdgeRule, error)
	ListEdgeRulesUsingCorsPreset(ctx context.Context, accountID, presetID string) ([]state.EdgeRule, error)
	ListRouteRulesTargeting(ctx context.Context, accountID, targetSlug string) ([]state.EdgeRule, error)
}

func exerciseEdgeRuleTargetedReads(t *testing.T, ctx context.Context, s edgeRuleTargetedReadsStore) {
	t.Helper()
	seed := func(suffix string) (state.Account, state.App) {
		acct, err := s.CreateAccount(ctx, "targeted-"+suffix+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "targeted-" + suffix, Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return acct, app
	}
	acct, app := seed("a")
	other, otherApp := seed("b")
	preset, err := s.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: acct.ID, Name: "shared",
		AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	cors := func(accountID, appID, host string, presetID *string) state.CreateEdgeRuleParams {
		return state.CreateEdgeRuleParams{AccountID: accountID, AppID: appID, MatchHost: host, MatchPath: "/", MatchMethods: []string{},
			Priority: 100, Enabled: true, Kind: state.EdgeRuleKindCORSA, CorsPresetID: presetID,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: presetID}}}
	}
	route := func(accountID, appID, host, target string) state.CreateEdgeRuleParams {
		p := pgSampleEdgeRuleParams(accountID, appID, host)
		p.Action.Route.TargetAppSlug = target
		return p
	}
	for _, in := range []state.CreateEdgeRuleParams{
		cors(acct.ID, app.ID, "preset.example.com", &preset.ID),
		cors(acct.ID, app.ID, "inline.example.com", nil),
		route(acct.ID, app.ID, "to-api.example.com", "api"),
		route(acct.ID, app.ID, "to-web.example.com", "web"),
		route(other.ID, otherApp.ID, "other-to-api.example.com", "api"),
	} {
		if _, err := s.CreateEdgeRule(ctx, in); err != nil {
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
		{"preset users", hosts(s.ListEdgeRulesUsingCorsPreset(ctx, acct.ID, preset.ID)), []string{"preset.example.com"}},
		{"preset of another account", hosts(s.ListEdgeRulesUsingCorsPreset(ctx, other.ID, preset.ID)), nil},
		{"unparsable preset id", hosts(s.ListEdgeRulesUsingCorsPreset(ctx, acct.ID, "not-a-preset")), nil},
		{"route rules to api", hosts(s.ListRouteRulesTargeting(ctx, acct.ID, "api")), []string{"to-api.example.com"}},
		{"route rules to web", hosts(s.ListRouteRulesTargeting(ctx, acct.ID, "web")), []string{"to-web.example.com"}},
		{"route rules to nothing", hosts(s.ListRouteRulesTargeting(ctx, acct.ID, "missing")), nil},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
