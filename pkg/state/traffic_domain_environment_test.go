// adr: 570
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestTrafficScopedDomainBindingBounds(t *testing.T) {
	environment := trafficHostEnvironment{ID: uuid.NewString(), App: uuid.NewString(), Present: true}
	host := "nested.api.example.test"
	domain := trafficHostDomain{Domain: "*.example.test", App: environment.App, Environment: environment.ID}
	for _, test := range []struct {
		name    string
		groups  []trafficHostGroup
		present bool
		scope   string
	}{
		{"empty-replacement", []trafficHostGroup{{App: environment.App, Pattern: host, Kind: string(EdgeRuleKindCORSA), Rows: 1, Canonical: 10, Compiled: api.TrafficPolicyMaxHostBytes}}, true, ""},
		{"fallback", []trafficHostGroup{{App: environment.App, Pattern: host, Kind: string(EdgeRuleKindCORSA), Rows: 1, Canonical: 10, Compiled: api.TrafficPolicyMaxHostBytes}}, false, "host_compiled_projection_estimate"},
		{"sibling-filter", []trafficHostGroup{{App: "sibling", Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: 1, Canonical: 10, Compiled: api.TrafficPolicyMaxHostBytes}}, true, ""},
		{"raw-before-filter", []trafficHostGroup{{App: "sibling", Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: 1, Canonical: api.TrafficPolicyMaxHostBytes, Compiled: 10}}, true, "host_rule_projection"},
		{"overlay-count", []trafficHostGroup{{App: environment.App, Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: api.TrafficPolicyMaxHostRules, Canonical: 10, Compiled: 10},
			{App: environment.App, Environment: environment.ID, Kind: string(EdgeRuleKindHeaders), Rows: 1, Canonical: 10, Compiled: 10}}, true, "host_compiled_rule_count"},
		{"overlay-by-binding", []trafficHostGroup{{App: environment.App, Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: 1, Canonical: 10, Compiled: api.TrafficPolicyMaxHostBytes - 100},
			{App: environment.App, Environment: environment.ID, Kind: string(EdgeRuleKindHeaders), Rows: 1, Canonical: 200, Compiled: 200}}, true, "host_compiled_projection_estimate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := trafficHostAnalysis{Groups: test.groups, Environments: []trafficHostEnvironment{environment}, Domains: []trafficHostDomain{domain}}
			view.Environments[0].Present = test.present
			if err := prepareTrafficEnvironmentHosts(&view); err != nil {
				t.Fatal(err)
			}
			err := checkTrafficHostAnalysis(t.Context(), trafficHostAnalysis{Groups: view.Groups, Environments: view.Environments}, view)
			if test.scope == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var aggregate *TrafficPolicyAggregateError
				if !errors.As(err, &aggregate) || aggregate.Scope != test.scope || aggregate.Host != host {
					t.Fatalf("new scoped binding: %v", err)
				}
			}
		})
	}
}

func TestTrafficScopedDomainOverlappingBindings(t *testing.T) {
	first := trafficHostEnvironment{ID: uuid.NewString(), App: uuid.NewString(), Present: true}
	second := trafficHostEnvironment{ID: uuid.NewString(), App: uuid.NewString(), Present: true}
	host := "api.example.test"
	for _, ordinary := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			view := trafficHostAnalysis{Environments: []trafficHostEnvironment{first, second},
				Groups: []trafficHostGroup{{App: second.App, Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: 1, Canonical: 10, Compiled: api.TrafficPolicyMaxHostBytes}},
				Domains: []trafficHostDomain{{Domain: "*.example.test", App: first.App, Environment: first.ID},
					{Domain: host, App: second.App, Environment: second.ID}}}
			if ordinary {
				view.Domains[1].Environment = ""
			}
			if reverse {
				view.Domains[0], view.Domains[1] = view.Domains[1], view.Domains[0]
			}
			if err := prepareTrafficEnvironmentHosts(&view); err != nil {
				t.Fatal(err)
			}
			var aggregate *TrafficPolicyAggregateError
			if err := checkTrafficHostAnalysis(t.Context(), trafficHostAnalysis{}, view); !errors.As(err, &aggregate) || aggregate.Host != host {
				t.Fatalf("overlap ordinary=%v reverse=%v lost a binding: %v", ordinary, reverse, err)
			}
		}
	}
}

var trafficScopedDomainModes = []string{"route", "wildcard", "sibling", "replacement", "fallback"}

func scopedDomainLegacyRule(account Account, app App, host string, kind EdgeRuleKind, size int) EdgeRule {
	action := EdgeRuleAction{Kind: kind, Validate: &EdgeRuleValidateAction{Schema: json.RawMessage(`"` + strings.Repeat("&", size) + `"`)}}
	if kind == EdgeRuleKindHeaders {
		action.Headers = &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Fallback", Action: "set", Value: "shared"}}}
	} else {
		action.Route = &EdgeRuleRouteAction{TargetAppSlug: app.Slug}
	}
	return EdgeRule{ID: "00000000-0000-0000-0000-000000000378", AccountID: account.ID, AppID: app.ID,
		MatchHost: host, MatchPath: "/", Enabled: true, Kind: kind, Action: action}
}

func testTrafficScopedDomainVerification(t *testing.T, store Store, account Account, project Project, app App, environment ProjectEnvironment, mode string, seed func(EdgeRule)) {
	t.Helper()
	domain, host := "scoped.example.test", "scoped.example.test"
	if mode == "wildcard" {
		domain, host = "*.example.test", "nested.api.example.test"
	}
	claim, err := store.(interface {
		CreateCustomDomainInEnvironmentIfUnderQuota(context.Context, string, string, string, string, int, int) (CustomDomain, error)
	}).CreateCustomDomainInEnvironmentIfUnderQuota(t.Context(), domain, app.ID, environment.ID, "token", 100, 500)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "replacement" {
		if _, err := store.PutProjectEnvironmentEdgePolicy(t.Context(), ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: environment.Slug}); err != nil {
			t.Fatal(err)
		}
	}
	owner := app
	if mode == "sibling" {
		owner, err = store.CreateApp(t.Context(), App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "peer", Slug: "scoped-peer"})
		if err != nil {
			t.Fatal(err)
		}
	}
	kind := EdgeRuleKindRoute
	if mode == "replacement" || mode == "fallback" {
		kind = EdgeRuleKindHeaders
	}
	rule := scopedDomainLegacyRule(account, owner, host, kind, api.TrafficPolicyMaxHostBytes/6+100)
	seed(rule)
	matched, err := store.(CustomDomainChallengeVerifier).MarkDomainVerifiedIfChallenge(t.Context(), domain, "token")
	if mode == "sibling" || mode == "replacement" {
		if err != nil || !matched {
			t.Fatalf("scoped compiler filtering: matched=%v err=%v", matched, err)
		}
		return
	}
	var aggregate *TrafficPolicyAggregateError
	if matched || !errors.As(err, &aggregate) || aggregate.Scope != "host_compiled_projection_estimate" || aggregate.Host != host {
		t.Fatalf("scoped publication accepted overload: matched=%v err=%v", matched, err)
	}
	if after, err := store.DomainByName(t.Context(), domain); err != nil || !reflect.DeepEqual(claim, after) {
		t.Fatalf("refused scoped publication changed domain: %+v/%v", after, err)
	}
	if err := store.DeleteEdgeRule(t.Context(), rule.ID); err != nil {
		t.Fatal(err)
	}
	if matched, err := store.(CustomDomainChallengeVerifier).MarkDomainVerifiedIfChallenge(t.Context(), domain, "token"); err != nil || !matched {
		t.Fatalf("scoped publication repair: matched=%v err=%v", matched, err)
	}
}

func TestMemTrafficScopedDomainVerification(t *testing.T) {
	for _, mode := range trafficScopedDomainModes {
		t.Run(mode, func(t *testing.T) {
			m, account, project, app, environment := memTrafficFixture(t)
			testTrafficScopedDomainVerification(t, m, account, project, app, environment, mode, func(rule EdgeRule) {
				// MemStore's raw JSON bound also conservatively counts HTML
				// escaping. Keep that raw projection below its ceiling and add
				// a retained preset to exceed the distinct compiled allowance.
				rule.Action.Validate.Schema = json.RawMessage(`"` + strings.Repeat("&", api.TrafficPolicyMaxHostBytes/6-5000) + `"`)
				m.edgeRules[rule.ID] = rule
				id := "scoped-cors-preset"
				m.corsPresets[id] = CorsPreset{ID: id, AccountID: account.ID, AppID: rule.AppID, AllowHeaders: []string{strings.Repeat("&", 7000)}}
				m.edgeRules["scoped-cors"] = EdgeRule{ID: "scoped-cors", AccountID: account.ID, AppID: rule.AppID, MatchHost: rule.MatchHost, Enabled: true, Kind: EdgeRuleKindCORSA,
					Action: EdgeRuleAction{Kind: EdgeRuleKindCORSA, CORS: &EdgeRuleCORSAction{CorsPresetID: &id}}}
			})
		})
	}
}

func testTrafficScopedDomainOverlayMutation(t *testing.T, store Store, account Account, project Project, app App, environment ProjectEnvironment, seed func(EdgeRule)) {
	t.Helper()
	host := "overlay.example.test"
	if _, err := store.(interface {
		CreateCustomDomainInEnvironmentIfUnderQuota(context.Context, string, string, string, string, int, int) (CustomDomain, error)
	}).CreateCustomDomainInEnvironmentIfUnderQuota(t.Context(), host, app.ID, environment.ID, "token", 100, 500); err != nil {
		t.Fatal(err)
	}
	rule := scopedDomainLegacyRule(account, app, host, EdgeRuleKindRoute, (api.TrafficPolicyMaxHostBytes-20000)/6)
	seed(rule)
	if matched, err := store.(CustomDomainChallengeVerifier).MarkDomainVerifiedIfChallenge(t.Context(), host, "token"); err != nil || !matched {
		t.Fatalf("initial scoped publication: %v/%v", matched, err)
	}
	before, err := store.DomainByName(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	policy := ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: environment.Slug}
	for range 20 {
		policy.Rules = append(policy.Rules, ProjectEnvironmentEdgeRule{Kind: EdgeRuleKindHeaders, Enabled: true, MatchPath: "/",
			Action: EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Scoped", Action: "set", Value: strings.Repeat("&", 4000)}}}}})
	}
	_, err = store.PutProjectEnvironmentEdgePolicy(t.Context(), policy)
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Host != host || aggregate.Scope != "host_compiled_projection_estimate" {
		t.Fatalf("overlay ignored active domain aggregate: %v", err)
	}
	if _, err := store.GetProjectEnvironmentEdgePolicy(t.Context(), account.ID, app.ID, environment.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused overlay changed intent: %v", err)
	}
	if after, err := store.DomainByName(t.Context(), host); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused overlay changed verified domain: %+v/%v", after, err)
	}
	if err := store.DeleteEdgeRule(t.Context(), rule.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
		t.Fatalf("overlay repair failed: %v", err)
	}
}

func TestMemTrafficScopedDomainOverlayMutation(t *testing.T) {
	m, account, project, app, environment := memTrafficFixture(t)
	testTrafficScopedDomainOverlayMutation(t, m, account, project, app, environment, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule })
}
