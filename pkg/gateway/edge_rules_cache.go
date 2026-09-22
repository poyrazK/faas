package gateway

import "github.com/onebox-faas/faas/pkg/state"

// Edge rule kind=cache subset (ADR-122 §Decision, see
// migrations/00321_edge_rules_kind_cache.sql).
//
// kind=cache is the per-route TTL primitive: a customer pins a
// fresh window + a stale-on-error window on a matched
// (host, path, method) tuple ("GET /catalog/* → 60 s fresh,
// 5 min stale-on-error"). The applier
// (handler.go's applyEdgeRuleCache, §4.1.2.17) consults the
// matched rule on every request; a fresh hit serves the cached
// body BEFORE the wake gate, so no VM runs and no `gb_ram_hour`
// accrues. A stale hit serves only on origin failure
// (wake gate failure or upstream 5xx/timeout), never on a
// normal cache miss.
//
// Auth posture: requests carrying `Authorization` or a session
// cookie are a hard bypass — they are NEVER stored and NEVER
// served. `vary_on` therefore accepts ONLY Accept-Language /
// Accept-Encoding; adding Authorization / Cookie to vary_on is
// rejected at create-time with 422
// (pkg/api/dto.go::EdgeRuleCacheAction.Validate).
//
// MaxAgeSeconds and StaleIfErrorSeconds are always > 0
// post-validate (apid's Validate applies the defaults 60 /
// 300, see api.EdgeRuleCacheAction.Validate and the
// ResponseCacheDefaultMaxAgeSeconds / ResponseCacheDefaultStaleIfErrorSeconds
// constants). A zero MaxAgeSeconds disables fresh hits but
// stale-on-error still applies within StaleIfErrorSeconds; a
// zero StaleIfErrorSeconds disables stale-on-error entirely
// (the applier then treats the entry as fresh-or-absent).
//
// The cmd-side compileCacheRules
// (cmd/gatewayd-internal/edge_rules.go) clamps out-of-range
// values as defence-in-depth against a direct-DB write that
// bypassed apid-Validate (the e2e helper seedEdgeRuleDirect
// at cmd/e2e/edge_rules_common_test.go:128 does exactly
// that).

// EdgeRuleCacheResolved is the kind=cache subset the gateway
// matcher reads on every request. Mirrors
// EdgeRuleBudgetResolved (pkg/gateway/edge_rules_budget.go:36)
// shape-for-shape minus the budget fields and plus the cache
// fields. Methods is a set (map[uppercase]bool) for O(1)
// method filter; the loader populates it from the row's
// MatchMethods slice.
//
// DeploymentID is NOT a per-rule field: the cache key binds to
// the live deployment of the current request (read by the
// applier from h.backend's resolved target, see
// pkg/gateway/handler.go::Handler.Port/DeploymentID at PR-B).
// A rule created under one deployment applies to the next one
// too — the deploymentID component of the cache key makes
// sure the previous release's bodies cannot bleed into the
// new release's window. cmd-side compileCacheRules leaves
// DeploymentID out of the Resolved struct entirely; the
// applier pulls it at request time.
type EdgeRuleCacheResolved struct {
	ID                          string
	AccountID                   string
	AppID                       string
	Priority                    int
	PathGlob                    string          // "" = any path
	Methods                     map[string]bool // nil = any method
	MaxAgeSeconds               int             // fresh window; 0 = no fresh hits
	StaleWhileRevalidateSeconds int             // serve stale immediately while refreshing; 0 = disabled
	StaleIfErrorSeconds         int             // post-fresh window; 0 = no stale-on-error
	VaryOn                      []string        // closed subset of {Accept-Language, Accept-Encoding}
}

// PickFirstCacheMatch is the priority-ASC + methods + path-glob
// filter used by cmd/gatewayd-internal/edge_rules.go's
// MatchCache after the cache returns the priority-ordered
// slice. Byte-for-byte mirror of PickFirstBudgetMatch
// (pkg/gateway/edge_rules_budget.go:63); the small copy keeps
// the per-kind return type precise without paying for a
// runtime-type assertion on every request.
//
// methods filter:
//
//   - empty map = any method matches
//   - non-empty map = request method MUST be present (case-folded
//     to upper by the loader)
//
// path glob: passed through stdlib path.Match; "" = match all;
// "*" = match all; "/catalog/*" = prefix-wildcard on the
// second segment.
func PickFirstCacheMatch(rules []EdgeRuleCacheResolved, requestPath, method string) *EdgeRuleCacheResolved {
	for i := range rules {
		r := &rules[i]
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

// toStateEdgeRuleCacheAction is the small adapter the cache
// applier uses to feed the runtime store. Mirrors the
// toStateEdgeRuleBudgetAction shape on the budget path
// (cmd/gatewayd-internal/edge_rules.go). Returns a fresh
// pointer each call so the cache entry's
// ruleAction field is independent of the resolved slice's
// lifetime; the applier uses the snapshot at Put() time.
func (r *EdgeRuleCacheResolved) toStateEdgeRuleCacheAction() *state.EdgeRuleCacheAction {
	if r == nil {
		return nil
	}
	// Defensive copies: VaryOn is a closed-vocab subset and
	// is small (≤ 2 entries) so a copy is essentially free,
	// but the resolved slice is shared across the per-host
	// cache (pkg/gateway/edge_rules.go::EdgeRuleCache.GetCache)
	// and a future in-place mutation must not alias the
	// stored action.
	vary := append([]string(nil), r.VaryOn...)
	return &state.EdgeRuleCacheAction{
		MaxAgeSeconds:               r.MaxAgeSeconds,
		StaleWhileRevalidateSeconds: r.StaleWhileRevalidateSeconds,
		StaleIfErrorSeconds:         r.StaleIfErrorSeconds,
		VaryOn:                      vary,
	}
}
