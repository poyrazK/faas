// adr: 375
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func analysisGroup(pattern string, size int64) trafficHostGroup {
	return trafficHostGroup{Pattern: pattern, Rows: 1, Canonical: size, Compiled: size}
}

func TestTrafficHostAnalysisEnvironmentReplacementAndReadBounds(t *testing.T) {
	environment := trafficHostEnvironment{ID: uuid.NewString(), App: uuid.NewString(), Present: true}
	base := trafficHostAnalysis{Environments: []trafficHostEnvironment{environment}}
	if err := prepareTrafficEnvironmentHosts(&base); err != nil {
		t.Fatal(err)
	}
	host := base.Environments[0].Host
	capBytes := int64(api.TrafficPolicyMaxHostBytes)
	for _, test := range []struct {
		name    string
		groups  []trafficHostGroup
		present bool
		scope   string
	}{
		{"explicit-empty-replaces", []trafficHostGroup{{App: environment.App, Pattern: host, Kind: string(EdgeRuleKindCORSA), Rows: 1, Canonical: 10, Compiled: capBytes}}, true, ""},
		{"absent-fallback", []trafficHostGroup{{App: environment.App, Pattern: host, Kind: string(EdgeRuleKindCORSA), Rows: 1, Canonical: 10, Compiled: capBytes}}, false, "host_compiled_projection_estimate"},
		{"sibling-compiler-excluded", []trafficHostGroup{{App: "sibling", Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: 1, Canonical: 10, Compiled: capBytes}}, true, ""},
		{"sibling-read-still-bounded", []trafficHostGroup{{App: "sibling", Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: 1, Canonical: capBytes, Compiled: 10}}, true, "host_rule_projection"},
		{"overlay-adds-to-route", []trafficHostGroup{{App: environment.App, Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: 1, Canonical: 10, Compiled: capBytes - 100},
			{App: environment.App, Environment: environment.ID, Rows: 1, Kind: string(EdgeRuleKindHeaders), Canonical: 200, Compiled: 200}}, true, "host_compiled_projection_estimate"},
		{"post-filter-count", []trafficHostGroup{{App: environment.App, Pattern: host, Kind: string(EdgeRuleKindRoute), Rows: api.TrafficPolicyMaxHostRules, Canonical: 10, Compiled: 10},
			{App: environment.App, Environment: environment.ID, Rows: 1, Kind: string(EdgeRuleKindHeaders), Canonical: 10, Compiled: 10}}, true, "host_compiled_rule_count"},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := trafficHostAnalysis{Groups: test.groups, Environments: []trafficHostEnvironment{environment}}
			view.Environments[0].Present = test.present
			if err := prepareTrafficEnvironmentHosts(&view); err != nil {
				t.Fatal(err)
			}
			err := checkTrafficHostAnalysis(t.Context(), trafficHostAnalysis{}, view)
			if test.scope == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var aggregate *TrafficPolicyAggregateError
			if !errors.As(err, &aggregate) || aggregate.Scope != test.scope {
				t.Fatalf("expected %s: %v", test.scope, err)
			}
		})
	}
}

func TestTrafficHostAnalysisEnvironmentPresetsAndScopeIsolation(t *testing.T) {
	first, second := trafficHostEnvironment{ID: uuid.NewString(), App: uuid.NewString(), Present: true}, trafficHostEnvironment{ID: uuid.NewString(), App: uuid.NewString(), Present: true}
	view := trafficHostAnalysis{Environments: []trafficHostEnvironment{first, second}, Assets: []trafficHostAsset{{ID: "replaced", Compiled: api.TrafficPolicyMaxHostBytes}},
		Groups: []trafficHostGroup{{App: first.App, Environment: first.ID, Rows: 1, Compiled: api.TrafficPolicyMaxHostBytes/2 + 100},
			{App: second.App, Environment: second.ID, Rows: 1, Compiled: api.TrafficPolicyMaxHostBytes/2 + 100}}}
	if err := prepareTrafficEnvironmentHosts(&view); err != nil {
		t.Fatal(err)
	}
	view.Groups = append(view.Groups, trafficHostGroup{App: first.App, Pattern: view.Environments[0].Host, Kind: string(EdgeRuleKindCORSA), Rows: 1, Compiled: 10, Preset: "replaced"})
	if err := checkTrafficHostAnalysis(t.Context(), trafficHostAnalysis{}, view); err != nil {
		t.Fatalf("disjoint overlays or replaced preset became account quota: %v", err)
	}
	bad := view
	bad.Environments = append([]trafficHostEnvironment(nil), view.Environments...)
	bad.Environments[0].ContractBytes = api.TrafficPolicyMaxContractBytes + 1
	var aggregate *TrafficPolicyAggregateError
	if err := checkTrafficHostAnalysis(t.Context(), view, bad); !errors.As(err, &aggregate) || aggregate.Scope != "environment_edge_policy" {
		t.Fatalf("overlay contract bound: %v", err)
	}
	view.Environments[0].ContractBytes = api.TrafficPolicyMaxContractBytes + 100
	if err := checkTrafficHostAnalysis(t.Context(), view, bad); err != nil {
		t.Fatalf("incremental contract repair: %v", err)
	}
}

func TestTrafficHostAnalysisRemovedEnvironmentDoesNotExposeFallback(t *testing.T) {
	before := trafficHostAnalysis{Environments: []trafficHostEnvironment{{ID: uuid.NewString(), App: uuid.NewString(), Present: true}}}
	if err := prepareTrafficEnvironmentHosts(&before); err != nil {
		t.Fatal(err)
	}
	before.Groups = []trafficHostGroup{{App: before.Environments[0].App, Pattern: "*", Kind: string(EdgeRuleKindCORSA), Rows: 1, Canonical: 10, Compiled: api.TrafficPolicyMaxHostBytes + 100}}
	after := trafficHostAnalysis{Groups: before.Groups}
	if err := checkTrafficHostAnalysis(t.Context(), before, after); err != nil {
		t.Fatalf("removing a registered URL exposed its unservable fallback: %v", err)
	}
}

func TestTrafficHostAnalysisOverlapAndLegacyRepair(t *testing.T) {
	capBytes := int64(api.TrafficPolicyMaxHostBytes)
	heavy := capBytes/2 + 100
	for _, test := range []struct {
		name          string
		before, after []trafficHostGroup
		refuse        bool
	}{
		{"exact", nil, []trafficHostGroup{analysisGroup("api.example.test", heavy), analysisGroup("api.example.test", heavy)}, true},
		{"wildcard-intersection", nil, []trafficHostGroup{analysisGroup("api.*", heavy), analysisGroup("*.test", heavy)}, true},
		{"disjoint", nil, []trafficHostGroup{analysisGroup("api.*", heavy), analysisGroup("web.*", heavy)}, false},
		{"no-transitive-quota", nil, []trafficHostGroup{analysisGroup("api.*", heavy), analysisGroup("web.*", heavy), analysisGroup("*", 50)}, false},
		{"question-unicode", nil, []trafficHostGroup{analysisGroup("?pi.test", heavy), analysisGroup("épi.test", heavy)}, true},
		{"percent-is-like", nil, []trafficHostGroup{analysisGroup("api.%", heavy), analysisGroup("api.test", heavy)}, true},
		{"underscore-is-like", nil, []trafficHostGroup{analysisGroup("_pi.test", heavy), analysisGroup("api.test", heavy)}, true},
		{"escaped-percent-disjoint", nil, []trafficHostGroup{analysisGroup(`api.\%`, heavy), analysisGroup("api.test", heavy)}, false},
		{"exact-escape-branch", nil, []trafficHostGroup{analysisGroup(`api.\%`, heavy), analysisGroup(`api.??`, heavy)}, true},
		{"repair", []trafficHostGroup{analysisGroup("api.*", capBytes+1000)}, []trafficHostGroup{analysisGroup("api.*", capBytes+500)}, false},
		{"unchanged-legacy-unrelated-host", []trafficHostGroup{analysisGroup("api.*", capBytes+1000)}, []trafficHostGroup{analysisGroup("api.*", capBytes+1000), analysisGroup("web.test", heavy)}, false},
		{"worse-legacy", []trafficHostGroup{analysisGroup("api.*", capBytes+1000)}, []trafficHostGroup{analysisGroup("api.*", capBytes+1001)}, true},
		{"move-to-new-host", []trafficHostGroup{analysisGroup("api.test", capBytes+1000)}, []trafficHostGroup{analysisGroup("web.test", capBytes+500)}, true},
		{"invalid-legacy-escape-repair", []trafficHostGroup{analysisGroup(`api.test\`, capBytes+1000)}, []trafficHostGroup{analysisGroup(`api.test\`, capBytes+500)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := checkTrafficHostAnalysis(t.Context(), trafficHostAnalysis{Groups: test.before}, trafficHostAnalysis{Groups: test.after})
			var aggregate *TrafficPolicyAggregateError
			if test.refuse {
				if !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit {
					t.Fatalf("expected aggregate refusal: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTrafficHostAnalysisDistinctPresetsAndDimensions(t *testing.T) {
	capBytes := int64(api.TrafficPolicyMaxHostBytes)
	for _, test := range []struct {
		name   string
		groups []trafficHostGroup
		assets []trafficHostAsset
		scope  string
	}{
		{"shared-once", []trafficHostGroup{{Pattern: "api.*", Preset: "shared", Rows: 1}, {Pattern: "*.test", Preset: "shared", Rows: 1}},
			[]trafficHostAsset{{"shared", capBytes/2 + 100}, {"unused", capBytes}}, ""},
		{"distinct", []trafficHostGroup{{Pattern: "api.*", Preset: "one", Rows: 1}, {Pattern: "*.test", Preset: "two", Rows: 1}},
			[]trafficHostAsset{{"one", capBytes/2 + 100}, {"two", capBytes/2 + 100}}, "host_compiled_projection_estimate"},
		{"count", []trafficHostGroup{{Pattern: "*", Rows: api.TrafficPolicyMaxHostRules + 1}}, nil, "host_rule_count"},
		{"canonical", []trafficHostGroup{{Pattern: "*", Rows: 1, Canonical: capBytes}}, nil, "host_rule_projection"},
		{"escaped-compiler-only", []trafficHostGroup{{Pattern: "*", Rows: 1, Canonical: 10, Compiled: capBytes}}, nil, "host_compiled_projection_estimate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := checkTrafficHostAnalysis(t.Context(), trafficHostAnalysis{}, trafficHostAnalysis{Groups: test.groups, Assets: test.assets})
			if test.scope == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var aggregate *TrafficPolicyAggregateError
			if !errors.As(err, &aggregate) || aggregate.Scope != test.scope {
				t.Fatalf("unexpected bound: %v", err)
			}
		})
	}
}

func TestTrafficHostAnalysisResourceRefusalAndCancellation(t *testing.T) {
	view := trafficHostAnalysis{Groups: []trafficHostGroup{analysisGroup("api.*", api.TrafficPolicyMaxHostBytes/2+100),
		analysisGroup("web.*", api.TrafficPolicyMaxHostBytes/2+100)}}
	for _, test := range []struct {
		name, scope string
		constrain   func(*hostAnalysisBudgets)
	}{
		{"nodes", "automaton_nodes", func(v *hostAnalysisBudgets) { v.nodes = 2 }},
		{"states", "states", func(v *hostAnalysisBudgets) { v.states = 2 }},
		{"bytes", "state_memory", func(v *hostAnalysisBudgets) { v.stateBytes = 1 }},
		{"transitions", "transitions", func(v *hostAnalysisBudgets) { v.transitions = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			budgets := trafficHostAnalysisBudgets()
			test.constrain(&budgets)
			err := checkTrafficHostAnalysisWithBudgets(t.Context(), trafficHostAnalysis{}, view, budgets)
			var limit *TrafficPolicyAnalysisError
			if !errors.As(err, &limit) || limit.Scope != test.scope || limit.Observed <= limit.Limit {
				t.Fatalf("expected bounded refusal: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := checkTrafficHostAnalysis(ctx, trafficHostAnalysis{}, trafficHostAnalysis{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fast path: %v", err)
	}
	if err := boundedTrafficPolicyAnalysis(ctx, func(context.Context) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation analysis: %v", err)
	}
}
