package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestRouteAdvicePolicySelectedHostAndRedaction(t *testing.T) {
	e := setup(t, api.PlanPro)
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "policy-advice"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "policy-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "other-policy-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	// Valid immutable host identities; no app-wide/default-host substitution.
	selection := routeDiagnosticSelection{Scope: "staging", Environment: environment}
	host := gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, selection.Environment.ID, app.ID)
	mk := func(appID, ruleHost string, enabled bool, headers map[string]string) state.EdgeRule {
		t.Helper()
		r, err := e.store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{AccountID: e.acct.ID, AppID: appID, MatchHost: ruleHost, MatchPath: "/**", Enabled: enabled, Kind: state.EdgeRuleKindCache, MatchHeaders: headers, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCache, Cache: &state.EdgeRuleCacheAction{MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	stage := mk(app.ID, host, true, map[string]string{"X-Private": "never-expose-condition"})
	wide := mk(app.ID, api.EdgeRuleAppHosts, true, nil)
	def := mk(app.ID, appHostForDomain(app.Slug, e.s.domain), true, nil)
	foreign := mk(other.ID, "*", true, nil)
	disabled := mk(app.ID, host, false, nil)
	snapshot := e.s.routeAdvicePolicy(t.Context(), app, selection, []string{host})
	if snapshot.context.Status != "observed" || len(snapshot.rules) != 2 || len(snapshot.context.Revision) != 64 {
		t.Fatalf("snapshot=%+v", snapshot.context)
	}
	data, _ := json.Marshal(snapshot.context)
	for _, private := range []string{def.ID, foreign.ID, disabled.ID, "never-expose-condition", "max_age_seconds", "X-Private"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("policy metadata leaked %q", private)
		}
	}
	if !strings.Contains(string(data), stage.ID) || !strings.Contains(string(data), wide.ID) {
		t.Fatal("missing selected-host/app-wide rule")
	}
	mk(other.ID, host, true, nil)
	again := e.s.routeAdvicePolicy(t.Context(), app, selection, []string{host})
	if again.context.Revision != snapshot.context.Revision {
		t.Fatal("another app's rules changed the scoped revision")
	}

	initial, err := e.store.GetEnvironmentCachePolicy(t.Context(), e.acct.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.SetEnvironmentCachePolicy(t.Context(), e.acct.ID, app.ID, "staging", api.SetEnvironmentCachePolicyRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: environment.ID, RequestID: uuid.NewString(), ExpectedRevision: &initial.Revision, Rules: []api.EnvironmentCacheRule{{Path: "/**", Priority: 1, Cache: api.EdgeRuleCacheAction{MaxAgeSeconds: 120}}}})
	if err != nil {
		t.Fatal(err)
	}
	overlay := e.s.routeAdvicePolicy(t.Context(), app, selection, []string{host})
	if overlay.context.Status != "observed" || len(overlay.rules) != 1 || overlay.rules[0].Metadata.Source != "environment_policy" || overlay.context.Revision == snapshot.context.Revision {
		t.Fatalf("advice ignored environment cache override: %+v", overlay.context)
	}
}

type advicePolicyFixture struct {
	state.Store
	rules        []state.EdgeRule
	fail, change bool
	failRecheck  bool
	reads        int
}

func (f *advicePolicyFixture) MatchEdgeRulesForHost(context.Context, string) ([]state.EdgeRule, error) {
	f.reads++
	if f.fail || f.failRecheck && f.reads > 1 {
		return nil, errors.New("never-expose-storage-detail")
	}
	out := append([]state.EdgeRule{}, f.rules...)
	if f.change && f.reads > 1 {
		out[0].UpdatedAt = out[0].UpdatedAt.Add(time.Second)
	}
	return out, nil
}
func (f *advicePolicyFixture) AppWideEdgeRules(context.Context, string) ([]state.EdgeRule, error) {
	return nil, nil
}

func TestRouteAdvicePolicyCoverageAndConcurrentChange(t *testing.T) {
	app := state.App{ID: "app", AccountID: "account"}
	base := state.EdgeRule{ID: "one", AppID: app.ID, AccountID: app.AccountID, Enabled: true, Kind: state.EdgeRuleKindCache, MatchPath: "/**", UpdatedAt: time.Now(), Action: state.EdgeRuleAction{Cache: &state.EdgeRuleCacheAction{MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}}}
	for _, tc := range []struct {
		name, want, gap     string
		fail, change, large bool
	}{
		{name: "read_error", want: "unavailable", gap: "host_policy_read_unavailable", fail: true},
		{name: "mutation", want: "changed", gap: "policy_changed_during_read", change: true},
		{name: "bounded", want: "unavailable", gap: "policy_rule_limit_exceeded", large: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &advicePolicyFixture{Store: state.NewMemStore(), rules: []state.EdgeRule{base}, fail: tc.fail, change: tc.change}
			if tc.large {
				for i := 0; i < api.RouteAdvicePolicyRuleLimit; i++ {
					r := base
					r.ID = fmt.Sprint(i)
					store.rules = append(store.rules, r)
				}
			}
			s := &server{store: store}
			suggestions := []api.RouteAdviceSuggestion{{Kind: "cache", Method: "GET", Route: "/catalog", Rules: []api.CreateEdgeRuleRequest{{Action: json.RawMessage(`{"max_age_seconds":60,"stale_if_error_seconds":300}`)}}}}
			got := s.assessScopedRouteAdvice(t.Context(), app, routeDiagnosticSelection{Scope: "default"}, []string{"app.example"}, suggestions)
			if got.Status != tc.want || len(got.Rules) != 0 || got.Revision != "" || len(got.Gaps) != 1 || got.Gaps[0] != tc.gap {
				t.Fatalf("coverage=%+v", got)
			}
			if suggestions[0].Policy.Status != "unavailable" || len(suggestions[0].Rules) != 0 || len(suggestions[0].Policy.RuleIDs) != 0 {
				t.Fatal("missing/changed evidence produced a policy claim")
			}
		})
	}
}

func TestRouteAdvicePolicyRecheckFailure(t *testing.T) {
	app := state.App{ID: "app", AccountID: "account"}
	store := &advicePolicyFixture{Store: state.NewMemStore(), failRecheck: true}
	s := &server{store: store}
	suggestions := []api.RouteAdviceSuggestion{{Kind: "cache", Method: "GET", Route: "/catalog"}}
	got := s.assessScopedRouteAdvice(t.Context(), app, routeDiagnosticSelection{Scope: "default"}, []string{"app.example"}, suggestions)
	if got.Status != "unavailable" || got.Gaps[0] != "policy_recheck_unavailable" || suggestions[0].Policy.Status != "unavailable" {
		t.Fatalf("failed verification became a change/absence claim: %+v", got)
	}
}
