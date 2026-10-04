// adr: 570
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the HTTP error and convergence contract independently of the
// numeric fixtures that verify the real store guard.
type refusingTrafficRuleStore struct {
	*state.MemStore
	refusal error
}

func (s refusingTrafficRuleStore) ruleRefusal() error {
	if s.refusal != nil {
		return s.refusal
	}
	return &state.TrafficPolicyProjectionError{Scope: "edge_rule", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 1}
}

func (s refusingTrafficRuleStore) CreateEdgeRuleIfUnderQuota(context.Context, state.CreateEdgeRuleParams, api.Limits) (state.EdgeRule, error) {
	return state.EdgeRule{}, s.ruleRefusal()
}

func (s refusingTrafficRuleStore) UpdateEdgeRule(context.Context, string, state.UpdateEdgeRuleParams) (state.EdgeRule, error) {
	return state.EdgeRule{}, s.ruleRefusal()
}

func TestTrafficRuleProjectionHTTPRefusalRetainsIntentAndAbortsConvergence(t *testing.T) {
	for _, fixture := range []struct {
		name, code string
		refusal    error
		bytes      bool
		limit      int64
	}{
		{"row", api.CodeTrafficPolicyTooLarge, nil, true, api.TrafficPolicyMaxHostBytes},
		{"aggregate-bytes", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "host_rule_projection", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 1}, true, api.TrafficPolicyMaxHostBytes},
		{"aggregate-count", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "host_rule_count", Unit: "rules", Limit: api.TrafficPolicyMaxHostRules, Observed: api.TrafficPolicyMaxHostRules + 1}, false, api.TrafficPolicyMaxHostRules},
		{"analysis", api.CodeTrafficPolicyTooComplex, &state.TrafficPolicyAnalysisError{Scope: "states", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: api.TrafficPolicyMaxAnalysisStates + 1}, false, api.TrafficPolicyMaxAnalysisStates},
		{"global-bytes", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "global_route_rule_projection", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 1}, true, api.TrafficPolicyMaxHostBytes},
		{"global-count", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "global_route_rule_count", Unit: "rules", Limit: api.TrafficPolicyMaxHostRules, Observed: api.TrafficPolicyMaxHostRules + 1}, false, api.TrafficPolicyMaxHostRules},
		{"global-analysis", api.CodeTrafficPolicyTooComplex, &state.TrafficPolicyAnalysisError{Scope: "global_route_database_time", Unit: "milliseconds", Limit: api.TrafficPolicyAnalysisSQLTimeout.Milliseconds(), Observed: api.TrafficPolicyAnalysisSQLTimeout.Milliseconds() + 1}, false, api.TrafficPolicyAnalysisSQLTimeout.Milliseconds()},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			for _, operation := range []string{"create", "update"} {
				t.Run(operation, func(t *testing.T) {
					env, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
					slug := mustSeedEdgeRuleApp(t, env, "guarded-rules")
					app, err := env.store.AppBySlug(t.Context(), slug)
					if err != nil {
						t.Fatal(err)
					}
					var original state.EdgeRule
					method, path, body := http.MethodPost, "/v1/apps/"+slug+"/edge-rules", any(edgeRuleRouteReq(slug))
					if operation == "update" {
						original, err = env.store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{AccountID: env.acct.ID,
							AppID: app.ID, Kind: state.EdgeRuleKindRoute, MatchHost: "api.example.com", MatchPath: "/", Enabled: true,
							Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: slug}}})
						if err != nil {
							t.Fatal(err)
						}
						method, path, body = http.MethodPatch, "/v1/edge-rules/"+original.ID, map[string]any{"priority": 25}
					}
					env.s.store = refusingTrafficRuleStore{MemStore: env.store, refusal: fixture.refusal}
					response := env.do(t, method, path, body, nil)
					var problem api.Problem
					if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
						t.Fatal(err)
					}
					if response.Code != http.StatusUnprocessableEntity || problem.Code != fixture.code || api.StatusForCode(problem.Code) != response.Code ||
						problem.Limit == nil || *problem.Limit != fixture.limit || problem.Observed == nil || *problem.Observed <= *problem.Limit ||
						fixture.bytes && (problem.LimitBytes == nil || *problem.LimitBytes != fixture.limit || problem.ObservedBytes == nil || *problem.ObservedBytes <= *problem.LimitBytes) ||
						!fixture.bytes && (problem.LimitBytes != nil || problem.ObservedBytes != nil) || problem.DocsURL == "" {
						t.Fatalf("rule projection problem: status=%d body=%s", response.Code, response.Body.String())
					}
					rules, err := env.store.ListEdgeRulesForApp(t.Context(), app.ID)
					if err != nil || operation == "create" && len(rules) != 0 || operation == "update" &&
						(len(rules) != 1 || rules[0].Priority != original.Priority || !rules[0].UpdatedAt.Equal(original.UpdatedAt)) {
						t.Fatalf("refused request changed intent: rules=%d err=%v", len(rules), err)
					}
					notifier.mu.Lock()
					defer notifier.mu.Unlock()
					aborted := false
					for _, event := range notifier.emitted {
						if event.Channel != db.NotifyEdgeRuleChanged {
							continue
						}
						var payload db.EdgeRuleChangedPayload
						if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
							t.Fatal(err)
						}
						if payload.Phase == "apply" {
							t.Fatal("refused write requested serving-fleet activation")
						}
						aborted = aborted || payload.Phase == "abort"
					}
					if !aborted {
						t.Fatal("refused write retained its convergence fence")
					}
				})
			}
		})
	}
}
