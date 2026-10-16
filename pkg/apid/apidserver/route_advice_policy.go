package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeadvisor"
	"github.com/onebox-faas/faas/pkg/state"
)

type routePolicySnapshot struct {
	context *api.RouteAdvicePolicyContext
	rules   []routeadvisor.PolicyRule
}

func unavailableRoutePolicy(reason string) routePolicySnapshot {
	return routePolicySnapshot{context: &api.RouteAdvicePolicyContext{Status: "unavailable", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Rules: []api.RouteAdvicePolicyRule{}, Gaps: []string{reason}}}
}

// Cache/async/throttle start with selected-host and @app rules. Independent
// named cache policy replaces cache rules only. Never borrow the default
// hostname's rules for a named environment, or expose another app's rules.
func (s *server) routeAdvicePolicy(ctx context.Context, app state.App, selection routeDiagnosticSelection, hosts []string) routePolicySnapshot {
	if len(hosts) != 1 || hosts[0] == "" {
		return unavailableRoutePolicy("selected_host_unavailable")
	}
	wideStore, ok := s.store.(state.AppWideEdgeRuleStore)
	if !ok {
		return unavailableRoutePolicy("app_wide_policy_reader_unavailable")
	}
	hostRules, err := s.store.MatchEdgeRulesForHost(ctx, hosts[0])
	if err != nil {
		return unavailableRoutePolicy("host_policy_read_unavailable")
	}
	wideRules, err := wideStore.AppWideEdgeRules(ctx, app.ID)
	if err != nil {
		return unavailableRoutePolicy("app_wide_policy_read_unavailable")
	}
	rules := state.MergeEdgeRulesInOrder(hostRules, wideRules)
	if selection.Scope != "" && selection.Scope != api.DefaultEnvScope {
		reader, ok := s.store.(state.EnvironmentCachePolicyReader)
		if !ok {
			return unavailableRoutePolicy("environment_cache_policy_reader_unavailable")
		}
		policy, err := reader.GetEnvironmentCachePolicy(ctx, app.AccountID, app.ID, selection.Scope)
		if err != nil || policy.EnvironmentID != selection.Environment.ID || policy.AppID != app.ID || policy.ProjectID != app.ProjectID {
			return unavailableRoutePolicy("environment_cache_policy_unavailable")
		}
		rules, err = state.ApplyEnvironmentCachePolicy(rules, policy, hosts[0])
		if err != nil {
			return unavailableRoutePolicy("environment_cache_policy_invalid")
		}
		for i := range rules {
			if rules[i].ManifestKey == "environment_cache_policy" {
				rules[i].AccountID = app.AccountID
			}
		}
	}
	snapshot := routePolicySnapshot{context: &api.RouteAdvicePolicyContext{Status: "observed", Host: hosts[0], ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Rules: []api.RouteAdvicePolicyRule{}, Gaps: []string{"other_gateway_gates_not_evaluated", "other_hosts_not_evaluated", "disabled_rules_not_evaluated", "gateway_convergence_not_verified", "runtime_request_conditions_not_supplied"}}}
	now := time.Now()
	for _, r := range rules {
		if r.AppID != app.ID || r.AccountID != app.AccountID || !r.Enabled || r.EdgeRuleExpired(now) || !advicePolicyKind(string(r.Kind)) {
			continue
		}
		if len(snapshot.rules) >= api.RouteAdvicePolicyRuleLimit {
			return unavailableRoutePolicy("policy_rule_limit_exceeded")
		}
		if r.UpdatedAt.IsZero() {
			return unavailableRoutePolicy("policy_revision_unavailable")
		}
		if r.Mode != "" && r.Mode != state.EdgeRuleModeEnforce && r.Mode != state.EdgeRuleModeLog {
			return unavailableRoutePolicy("policy_mode_unavailable")
		}
		rule := advicePolicyRule(r)
		if r.Kind == state.EdgeRuleKindCache && r.Action.Cache != nil && len(rule.Metadata.Methods) == 0 {
			continue
		}
		snapshot.rules = append(snapshot.rules, rule)
		snapshot.context.Rules = append(snapshot.context.Rules, rule.Metadata)
	}
	// A scoped observation token, not an account-wide activity counter or a
	// write guard. Never hash private action/condition values into this token.
	revision, _ := json.Marshal(struct {
		App, Scope, Environment, Host string
		Rules                         []api.RouteAdvicePolicyRule
	}{app.ID, selection.Scope, selection.Environment.ID, hosts[0], snapshot.context.Rules})
	sum := sha256.Sum256(revision)
	snapshot.context.Revision = hex.EncodeToString(sum[:])
	return snapshot
}

func advicePolicyKind(kind string) bool {
	return kind == api.RouteAdviceKindCache || kind == api.RouteAdviceKindAsync || kind == api.RouteAdviceKindThrottle
}

func advicePolicyRule(r state.EdgeRule) routeadvisor.PolicyRule {
	source := "host"
	if api.IsEdgeRuleAppHosts(r.MatchHost) {
		source = "app_wide"
	}
	if r.ManifestKey == "environment_cache_policy" {
		source = "environment_policy"
	}
	mode := r.Mode
	if mode == "" {
		mode = state.EdgeRuleModeEnforce
	}
	methods := append([]string{}, r.MatchMethods...)
	if r.Kind == state.EdgeRuleKindCache && r.Action.Cache != nil {
		methods = []string{}
		for _, method := range []string{"GET", "HEAD"} {
			if (len(r.MatchMethods) == 0 || slices.Contains(r.MatchMethods, method)) && (len(r.Action.Cache.Methods) == 0 || slices.Contains(r.Action.Cache.Methods, method)) {
				methods = append(methods, method)
			}
		}
	}
	out := routeadvisor.PolicyRule{Metadata: api.RouteAdvicePolicyRule{ID: r.ID, Kind: string(r.Kind), Source: source, Path: r.MatchPath, Methods: methods, Priority: r.Priority, Conditional: len(r.MatchHeaders) > 0 || r.Match != nil, Mode: mode, UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339Nano)}}
	if r.ExpiresAt != nil {
		out.Metadata.ExpiresAt = r.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	switch string(r.Kind) {
	case api.RouteAdviceKindCache:
		out.Action, _ = json.Marshal(r.Action.Cache)
	case api.RouteAdviceKindAsync:
		out.Action, _ = json.Marshal(r.Action.Async)
	case api.RouteAdviceKindThrottle:
		out.Action, _ = json.Marshal(r.Action.Throttle)
	}
	return out
}

func (s *server) assessScopedRouteAdvice(ctx context.Context, app state.App, selection routeDiagnosticSelection, hosts []string, suggestions []api.RouteAdviceSuggestion) *api.RouteAdvicePolicyContext {
	snapshot := s.routeAdvicePolicy(ctx, app, selection, hosts)
	for i := range suggestions {
		suggestions[i].Policy = routeadvisor.AssessPolicy(suggestions[i], snapshot.rules, snapshot.context.Status)
		suggestions[i].Rules = []api.CreateEdgeRuleRequest{}
		suggestions[i].Cautions = append(suggestions[i].Cautions, "Policy assessment observes selected-app desired rules only. It does not establish gateway convergence or authorize a change; no rule bodies are provided.")
	}
	current := s.routeAdvicePolicy(ctx, app, selection, hosts)
	if snapshot.context.Status == "observed" && (current.context.Status != "observed" || current.context.Revision != snapshot.context.Revision) {
		if current.context.Status != "observed" {
			snapshot = unavailableRoutePolicy("policy_recheck_unavailable")
		} else {
			snapshot = unavailableRoutePolicy("policy_changed_during_read")
			snapshot.context.Status = "changed"
		}
		for i := range suggestions {
			suggestions[i].Policy = routeadvisor.AssessPolicy(suggestions[i], nil, snapshot.context.Status)
		}
	}
	return snapshot.context
}
