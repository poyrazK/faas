package gateway

import "net/http"

// Edge rule kind=budget subset (ADR-093 §Decision, see
// migrations/00254_edge_rules_kind_budget.sql).
//
// kind=budget is the per-request wall-clock budget primitive: a
// customer pins a hard wall-clock deadline on a matched
// (host, path, method) tuple ("POST /payment → 3 s"). The
// applier (handler.go's applyEdgeRuleBudget, §4.1.2.8d) stamps
// the per-request budget onto r.Context() via
// `reqbudget.WithRemaining` so every downstream hop (JWT verify,
// forward, gRPC, DB) propagates remaining time via
// `reqbudget.WithOverhead` / `WithCeiling`. Deadline fire
// surfaces as 504 + RFC 7807 `code: request_budget_exceeded`.
//
// BudgetMs is always > 0 post-compile — the cmd-side
// compileBudgetRules (cmd/gatewayd-internal/edge_rules.go) clamps
// non-positive or > ceiling values to api.RequestBudgetMaxMs as
// defence-in-depth against a direct-DB write that bypassed
// apid-Validate (the e2e helper seedEdgeRuleDirect at
// cmd/e2e/edge_rules_common_test.go:128 does exactly that).
//
// AllowOverrideHeader is the optional per-customer-tunable knob:
// when set, the gateway reads the named HTTP header on the inbound
// request and, if it parses as a positive integer ≤
// api.RequestBudgetMaxMs, uses that value as the per-request budget
// for that single request. Empty falls through to the platform
// default `x-faas-budget-ms`.

// EdgeRuleBudgetResolved is the kind=budget subset the gateway
// matcher reads on every request. Mirrors EdgeRuleLimitResolved
// (pkg/gateway/edge_rules_limit.go:45) shape-for-shape minus the
// body-cap fields the limit applier needs and plus the
// BudgetMs + AllowOverrideHeader fields the budget applier
// needs.
type EdgeRuleBudgetResolved struct {
	ID                  string
	AccountID           string
	AppID               string
	Priority            int
	PathGlob            string          // "" = any path
	Methods             map[string]bool // nil = any method
	MatchHeaders        map[string]string
	BudgetMs            int    // always > 0 post-compile
	AllowOverrideHeader string // "" = platform default x-faas-budget-ms
}

// PickFirstBudgetMatch is the priority-ASC + methods + path-glob
// filter used by cmd/gatewayd-internal/edge_rules.go's MatchBudget
// after the cache returns the priority-ordered slice. Byte-for-byte
// mirror of PickFirstLimitMatch (pkg/gateway/edge_rules_limit.go:71);
// the small copy keeps the per-kind return type precise without
// paying for a runtime-type assertion on every request.
//
// methods filter:
//
//   - empty map = any method matches
//   - non-empty map = request method MUST be present (case-folded
//     to upper by the loader)
//
// path glob: passed through stdlib path.Match; "" = match all;
// "*" = match all; "/v1/payment/*" = prefix-wildcard on the second
// segment.
func PickFirstBudgetMatch(rules []EdgeRuleBudgetResolved, requestPath, method string, requestHeaders ...http.Header) *EdgeRuleBudgetResolved {
	for i := range rules {
		r := &rules[i]
		if len(requestHeaders) > 0 && !RequestHeaderConditionsMatch(r.MatchHeaders, requestHeaders[0]) {
			continue
		}
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, requestPath)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}
