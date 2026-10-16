package routeadvisor

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPolicyAssessmentUsesSelectorsAndUncertainty(t *testing.T) {
	suggestion := api.RouteAdviceSuggestion{Kind: "cache", Method: "GET", Route: "/catalog/items/1", Rules: []api.CreateEdgeRuleRequest{{Action: json.RawMessage(`{"max_age_seconds":60,"stale_if_error_seconds":300}`)}}}
	rule := PolicyRule{Metadata: api.RouteAdvicePolicyRule{ID: "cache", Kind: "cache", Path: "/catalog/*", Methods: []string{"GET"}, Priority: 10, Mode: "enforce"}, Action: json.RawMessage(`{"stale_if_error_seconds":300,"max_age_seconds":60}`)}
	for _, tc := range []struct {
		name, status, want string
		edit               func(*api.RouteAdviceSuggestion, *[]PolicyRule)
	}{
		{name: "duplicate", status: "observed", want: "duplicate"},
		{name: "different_settings", status: "observed", want: "conflict", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) {
			(*r)[0].Action = json.RawMessage(`{"max_age_seconds":300}`)
		}},
		{name: "method_mismatch", status: "observed", want: "no_matching_rule", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) { (*r)[0].Metadata.Methods = []string{"POST"} }},
		{name: "path_mismatch", status: "observed", want: "no_matching_rule", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) { (*r)[0].Metadata.Path = "/private/*" }},
		{name: "header_condition", status: "observed", want: "review_required", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) { (*r)[0].Metadata.Conditional = true }},
		{name: "log_only", status: "observed", want: "review_required", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) { (*r)[0].Metadata.Mode = "log" }},
		{name: "priority_tie", status: "observed", want: "review_required", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) {
			other := (*r)[0]
			other.Metadata.ID = "second"
			*r = append(*r, other)
		}},
		{name: "related_async", status: "observed", want: "review_required", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) { (*r)[0].Metadata.Kind = "async" }},
		{name: "route_template", status: "observed", want: "review_required", edit: func(s *api.RouteAdviceSuggestion, _ *[]PolicyRule) { s.Route = "/catalog/{id}" }},
		{name: "bad_path", status: "observed", want: "review_required", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) { (*r)[0].Metadata.Path = "[" }},
		{name: "bad_action", status: "observed", want: "review_required", edit: func(_ *api.RouteAdviceSuggestion, r *[]PolicyRule) { (*r)[0].Action = json.RawMessage(`null`) }},
		{name: "storage_error", status: "unavailable", want: "unavailable"},
		{name: "concurrent_change", status: "changed", want: "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := suggestion
			rules := []PolicyRule{rule}
			if tc.edit != nil {
				tc.edit(&s, &rules)
			}
			got := AssessPolicy(s, rules, tc.status)
			if got.Status != tc.want {
				t.Fatalf("assessment=%+v", got)
			}
		})
	}
}

func TestPolicyAssessmentBoundsAndProtectivePaths(t *testing.T) {
	s := api.RouteAdviceSuggestion{Kind: "throttle", Method: "GET", Route: "/ADMIN/users", Rules: []api.CreateEdgeRuleRequest{{Action: json.RawMessage(`{"burst":10}`)}}}
	var rules []PolicyRule
	for i := 0; i < api.RouteAdvicePolicyMatchLimit+2; i++ {
		rules = append(rules, PolicyRule{Metadata: api.RouteAdvicePolicyRule{ID: fmt.Sprint(i), Kind: "throttle", Path: "/admin/**", Mode: "enforce", Priority: i}, Action: json.RawMessage(`{"burst":10}`)})
	}
	got := AssessPolicy(s, rules, "observed")
	if got.Status != "duplicate" || !got.Truncated || len(got.RuleIDs) != api.RouteAdvicePolicyMatchLimit {
		t.Fatalf("protective/bounded policy=%+v", got)
	}
}
