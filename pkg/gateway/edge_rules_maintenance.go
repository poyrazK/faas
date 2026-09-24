package gateway

import "net/http"

// Edge rule kind=maintenance subset (ADR-091 amendment, see
// migrations/00224_edge_rules_kind_maintenance.sql).
//
// kind=maintenance is the fine-grained 503 primitive: a customer
// marks a specific (match_host, match_path, match_methods) tuple
// as returning 503 + Retry-After. Distinct from the coarse sibling
// apps.maintenance_mode (§4.1.2.0) which fires on the whole app.
// The applier (handler.go::applyEdgeRuleMaintenance, §4.1.2.13)
// short-circuits a matched request with 503 BEFORE auth, BEFORE
// wake — the cheapest possible deny path. Audit + outcome metric
// are emitted via the standard EdgeRuleAuditor and
// ObserveEdgeRuleMatch("maintenance", "match|miss|blocked") hooks.
//
// RetryAfterSeconds is the per-rule Retry-After override. 0 means
// "use the platform default" (api.EdgeRuleMaintenanceRetryAfterSeconds
// = 60 s at PR-A); the cmd-side compileMaintenanceRules clamps
// non-positive values to the default as defence-in-depth against a
// direct-DB write that bypassed apid-Validate (the e2e helper
// seedEdgeRuleDirect at cmd/e2e/edge_rules_common_test.go:128 does
// exactly that). MaxEdgeRuleMaintenanceRetryAfterSeconds (24 h,
// enforced at apid-Validate time) is the hard cap; values above
// the cap never reach this struct because apid-Validate rejects them
// with 422 before the row is written.
//
// Message is the optional human-readable detail that surfaces as
// Problem.detail on the 503. "" = the generic Problem body. The
// 512-byte cap is enforced at apid-Validate time (mirrors
// EdgeRuleValidateAction.MaxBodyBytes' Validate contract).

// EdgeRuleMaintenanceResolved is the kind=maintenance subset the
// gateway matcher reads on every request. Mirrors EdgeRuleValidateResolved
// (pkg/gateway/edge_rules.go:258) and EdgeRuleLimitResolved
// (pkg/gateway/edge_rules_limit.go:45) shape-for-shape minus the
// body-shape fields the validate/limit appliers need and plus the
// two fields the maintenance applier needs (RetryAfterSeconds +
// Message).
//
// RetryAfterSeconds is always > 0 post-compile — the cmd-side
// compileMaintenanceRules (cmd/gatewayd-internal/edge_rules.go)
// clamps non-positive values to api.EdgeRuleMaintenanceRetryAfterSeconds
// as defence-in-depth against a direct-DB write that bypassed
// apid-Validate.
//
// Message may be empty (the generic Problem body is fine for v1);
// the 512-byte cap is apid-Validate-only.
type EdgeRuleMaintenanceResolved struct {
	ID                string
	AccountID         string
	AppID             string
	Priority          int
	PathGlob          string          // "" = any path
	Methods           map[string]bool // nil = any method
	MatchHeaders      map[string]string
	RetryAfterSeconds int    // always > 0 post-compile
	Message           string // optional, ≤512 B apid-validated
}

// PickFirstMaintenanceMatch is the priority-ASC + methods + path-glob
// filter used by cmd/gatewayd-internal/edge_rules.go's MatchMaintenance
// after the cache returns the priority-ordered slice. Byte-for-byte
// mirror of PickFirstValidateMatch (pkg/gateway/edge_rules.go:920) —
// the small copy keeps the per-kind return type precise without
// paying for a runtime-type assertion on every request.
//
// methods filter:
//
//   - empty map = any method matches
//   - non-empty map = request method MUST be present (case-folded
//     to upper by the loader)
//
// path glob: passed through the local pathGlobMatch helper (see
// pkg/gateway/edge_rules.go); "" = match all; "*" = match all;
// "/api/*" = prefix-wildcard on the second segment.
func PickFirstMaintenanceMatch(rules []EdgeRuleMaintenanceResolved, requestPath, method string, requestHeaders ...http.Header) *EdgeRuleMaintenanceResolved {
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
