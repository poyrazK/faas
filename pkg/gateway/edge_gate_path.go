package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// publicRequestPath is the path the client sent: ServeHTTP records it before
// any rewrite rule changes r.URL.Path. A request that never reached that
// point (unit tests calling a gate directly) uses r.URL.Path.
func publicRequestPath(r *http.Request) string {
	if route, ok := r.Context().Value(lifecycleRequestRouteKey{}).(lifecycleRequestRoute); ok && route.path != "" {
		return route.path
	}
	return r.URL.Path
}

// edgeGateRule is a resolved access-gate rule (ip, geo, jwt, throttle, waf,
// challenge): a pointer the matcher owns, identified by its rule ID.
type edgeGateRule interface {
	comparable
	edgeGateRuleID() string
}

func (r *EdgeRuleIPResolved) edgeGateRuleID() string        { return r.ID }
func (r *EdgeRuleGeoResolved) edgeGateRuleID() string       { return r.ID }
func (r *EdgeRuleJWTResolved) edgeGateRuleID() string       { return r.ID }
func (r *EdgeRuleThrottleResolved) edgeGateRuleID() string  { return r.ID }
func (r *EdgeRuleWAFResolved) edgeGateRuleID() string       { return r.ID }
func (r *EdgeRuleChallengeResolved) edgeGateRuleID() string { return r.ID }

// matchEdgeGates returns an access gate's rules for a request: the rule for
// the path the client sent and, when a rewrite rule changed the path, the
// rule for the rewritten path, client path first and each rule once. The
// gate enforces every rule returned.
//
// Rewrite runs before these gates. Review round 8 (ADR-091 amendment
// 2026-11-02) made them match the client's path first, so a jwt rule on
// /api/** kept applying after a rewrite took /api away, but it enforced only
// that one rule: a rewritten-path rule (an ip allowlist on /internal/**)
// stopped applying whenever any rule matched the client's path. Review round
// 9 (amendment 2026-11-03) enforces both.
func matchEdgeGates[R edgeGateRule](r *http.Request, match func(ctx context.Context, path string) R) []R {
	var zero R
	ctx := r.Context()
	public := publicRequestPath(r)
	var rules []R
	if rule := match(ctx, public); rule != zero {
		rules = append(rules, rule)
	}
	if r.URL.Path == public {
		return rules
	}
	rule := match(ctx, r.URL.Path)
	if rule == zero || (len(rules) == 1 && rules[0].edgeGateRuleID() == rule.edgeGateRuleID()) {
		return rules
	}
	return append(rules, rule)
}

// throttleJWTClaimNames is the custom claim each same-account jwt_claim
// throttle matching the request keys by, for the JWT gate to extract.
func (h *Handler) throttleJWTClaimNames(r *http.Request, app App) []string {
	if h.edgeRules == nil {
		return nil
	}
	var names []string
	for _, throttle := range matchEdgeGates(r, func(ctx context.Context, path string) *EdgeRuleThrottleResolved {
		return h.edgeRules.MatchThrottle(ctx, hostname(r.Host), path, r.Method)
	}) {
		if throttle.AccountID == app.AccountID && throttle.KeyBy == api.ThrottleKeyByJWTClaim && throttle.JWTClaimName != "" {
			names = append(names, throttle.JWTClaimName)
		}
	}
	return names
}

// mergeJWTClaims adds the claims a later jwt rule extracted from the same
// bearer token to those an earlier rule extracted.
func mergeJWTClaims(prior, next map[string]string) map[string]string {
	if len(prior) == 0 {
		return next
	}
	merged := make(map[string]string, len(prior)+len(next))
	for k, v := range prior {
		merged[k] = v
	}
	for k, v := range next {
		merged[k] = v
	}
	return merged
}
