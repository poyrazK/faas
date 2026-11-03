package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

type edgeRuleMethodOverrideContextKey struct{}

// WithEdgeRuleMethodOverride records the method the inbound request asks the
// app to run it as (api.EdgeRuleMethodOverride), from the original headers
// and query, before any rewrite action runs. "" records nothing.
func WithEdgeRuleMethodOverride(ctx context.Context, r *http.Request) context.Context {
	override := api.EdgeRuleMethodOverride(r.Method, r.Header, r.URL.RawQuery)
	if override == "" {
		return ctx
	}
	return context.WithValue(ctx, edgeRuleMethodOverrideContextKey{}, override)
}

// EdgeRuleMethodOverride returns the override method recorded by
// WithEdgeRuleMethodOverride, or "".
func EdgeRuleMethodOverride(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	override, _ := ctx.Value(edgeRuleMethodOverrideContextKey{}).(string)
	return override
}

// edgeRulePrioritized is implemented by the resolved kinds that deny or
// constrain requests.
type edgeRulePrioritized interface {
	edgeRulePriority() int
}

func (r *EdgeRuleJWTResolved) edgeRulePriority() int         { return r.Priority }
func (r *EdgeRuleIPResolved) edgeRulePriority() int          { return r.Priority }
func (r *EdgeRuleValidateResolved) edgeRulePriority() int    { return r.Priority }
func (r *EdgeRuleGeoResolved) edgeRulePriority() int         { return r.Priority }
func (r *EdgeRuleLimitResolved) edgeRulePriority() int       { return r.Priority }
func (r *EdgeRuleThrottleResolved) edgeRulePriority() int    { return r.Priority }
func (r *EdgeRuleMaintenanceResolved) edgeRulePriority() int { return r.Priority }
func (r *EdgeRuleWAFResolved) edgeRulePriority() int         { return r.Priority }
func (r *EdgeRuleChallengeResolved) edgeRulePriority() int   { return r.Priority }

// EdgeRulePicker is the PickFirst*Match shape: the first rule in priority
// order whose selectors cover the request.
type EdgeRulePicker[T any] func(rules []T, requestPath, method string, requestHeaders ...http.Header) *T

// MatchProtectiveEdgeRule selects the enforced and log-mode rule of a kind
// that denies or constrains requests, records the matches
// (ObserveEdgeRuleMatch), and returns the enforced one. Frameworks run a POST
// carrying X-HTTP-Method-Override: DELETE or ?_method=DELETE as DELETE, so a
// gate scoped to DELETE also matches the overridden method: the rule selected
// is the higher-priority of the request method's match and the override's,
// and the request method's wins a tie (ADR-091 amendment 2026-11-02, later).
// Conditions see the method each pass selects for.
func MatchProtectiveEdgeRule[T any](ctx context.Context, rules []T, account func(*T) string, requestPath, method string, pick EdgeRulePicker[T]) *T {
	headers := EdgeRuleRequestHeaders(ctx)
	override := EdgeRuleMethodOverride(ctx)
	if override == method {
		override = ""
	}
	first := func(filter func(context.Context, []T, func(*T) string, string, string) []T) *T {
		best := pick(filter(ctx, rules, account, requestPath, method), requestPath, method, headers)
		if override == "" {
			return best
		}
		alt := pick(filter(ctx, rules, account, requestPath, override), requestPath, override, headers)
		if alt != nil && (best == nil || protectivePriority(alt) < protectivePriority(best)) {
			return alt
		}
		return best
	}
	return ObserveEdgeRuleMatch(ctx, first(ApplicableEdgeRules[T]), first(LoggedEdgeRules[T]))
}

func protectivePriority[T any](rule *T) int {
	if p, ok := any(rule).(edgeRulePrioritized); ok {
		return p.edgeRulePriority()
	}
	return 0
}
