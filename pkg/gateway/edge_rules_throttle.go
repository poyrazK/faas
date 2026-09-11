package gateway

// Edge rule kind=throttle subset (ADR-091 D20.5 amendment, see
// migrations/00244_edge_rules_kind_throttle.sql; issue #881).
//
// kind=throttle is the per-route token-bucket rate limit primitive
// a customer attaches to one (match_host, match_path, match_methods)
// triple. Distinct from kind=limit (which is a body-size cap, 413,
// PR #845). Distinct from the plan-tier shared limiters at
// pkg/gateway/ratelimit.go:57 (per-app) and :75 (per-account),
// which are platform-controlled and plan-keyed; here the customer
// chooses a non-null RPS+burst that tightens the shared
// plan.RateLimitRPS ceiling but never raises above it. A 429
// response with `Retry-After: 1` +
// `x-faas-rate-limit-scope: route` +
// `X-RouteRateLimit-{Limit,Remaining,Reset}` is emitted when the
// rule fires.
//
// The applier (handler.go::applyEdgeRuleThrottle, §4.1.2.x) lives
// on the wake path between kind=limit (the body-size cap) and
// kind=validate (the schema gate): requests already denied by
// JWT/IP/Geo/Limit must NOT consume a route token, so the throttle
// sits AFTER them; the O(1) bucket lookup MUST run before the
// validate applier allocates and parses r.Body, so the throttle
// sits BEFORE validate; the bucket decrement is bounded by the
// rule's burst so a sudden spike cannot starve legitimate
// traffic. Audit + outcome metric emitted via the standard
// EdgeRuleAuditor and ObserveEdgeRuleMatch("throttle",
// "match|miss|blocked") hooks.
//
// Sub-plan ceiling (rps <= plan.RateLimitRPS, burst <= plan.
// RateLimitBurst) is enforced twice: at apid create/update by
// api.EdgeRuleThrottleAction.Validate, and again at gateway
// compile time by cmd/gatewayd-internal/edge_rules.go::
// compileThrottleRules as defence-in-depth against a direct-DB
// write that bypassed apid-Validate (the e2e helper
// seedEdgeRuleDirect at cmd/e2e/edge_rules_common_test.go does
// exactly that). The compile clamps non-positive values to
// {1 rps, 1 burst} as belt-and-braces against a 0/0 direct-DB
// seed; the apid hard caps reject > plan ceiling.

// EdgeRuleThrottleResolved is the kind=throttle subset the gateway
// matcher reads on every request. Mirrors EdgeRuleLimitResolved
// (pkg/gateway/edge_rules_limit.go) shape-for-shape minus the body
// cap and plus the two fields the throttle applier needs
// (RequestsPerSecond + Burst). The bucket key the limiter uses is
// AppID + "\x00" + ID, so cardinality is bounded by *configured
// rules* — not by traffic — and the LRU discipline in
// pkg/gateway/ratelimit.go (Limiter.NewLimiterWithLRU, PR #887)
// can never evict a partially-drained bucket and so cannot be
// weaponised as a limit-bypass.
//
// RequestsPerSecond and Burst are always > 0 post-compile — the
// cmd-side compileThrottleRules (cmd/gatewayd-internal/edge_rules.go)
// clamps to {1, 1} as defence-in-depth. A cap above the apid-validate
// ceiling (plan.RateLimitRPS, plan.RateLimitBurst) never reaches
// this struct because compileThrottleRules log-warns + clamps — the
// same posture compileLimitRules takes for kind=limit.
//
// Phase 3 (ADR-091 D20.5 amendment 4, ADR-104, issue #881 Phase 3)
// extends the resolved shape with three per-consumer keying fields.
// The applier (handler.go::applyEdgeRuleThrottle) reads these and
// routes the bucket-key construction through Limiter.AllowWithParams
// (back-compat, KeyBy == "" or "none") or Limiter.AllowWithConsumerKey
// (per-consumer, KeyBy ∈ {"api_key","consumer_id","jwt_subject","jwt_claim"}):
//
//   - KeyBy           closed vocab from api.ThrottleKeyBy*.
//     Empty / "none" preserves PR #887 behaviour.
//   - JWTClaimName    required iff KeyBy == "jwt_claim"; named JWT
//     custom claim to extract from the request.
//   - MaxKeysPerRule  caps the per-rule consumer-set cardinality;
//     over-cap consumers collapse into a single
//     non-evicting __other__ bucket that still
//     consumes tokens (ADR-104 §Consequences).
//     0 = use plan default at apply time (resolver
//     in cmd-side compileThrottleRules).
type EdgeRuleThrottleResolved struct {
	ID                string
	AccountID         string
	AppID             string
	Priority          int
	PathGlob          string          // "" = any path
	Methods           map[string]bool // nil = any method
	RequestsPerSecond float64         // > 0 post-compile
	Burst             int             // > 0 post-compile
	// Phase 3 (ADR-104):
	KeyBy          string // "" | "none" | "api_key" | "consumer_id" | "jwt_subject" | "jwt_claim"
	JWTClaimName   string // required iff KeyBy == "jwt_claim"
	MaxKeysPerRule int    // 0 = plan default; capped at compileThrottleRules
}

// PickFirstThrottleMatch is the priority-ASC + methods +
// path-glob filter used by cmd/gatewayd-internal/edge_rules.go's
// MatchThrottle after the cache returns the priority-ordered
// slice. Byte-for-byte mirror of PickFirstMaintenanceMatch
// (pkg/gateway/edge_rules_maintenance.go:75) — the small copy
// keeps the per-kind return type precise without paying for a
// runtime-type assertion on every request.
//
// methods filter:
//
//   - empty map = any method matches
//   - non-empty map = request method MUST be present
//
// path glob: passed through the local pathGlobMatch helper; "" =
// match all; "*" = match all; "/api/*" = prefix-wildcard on the
// second segment.
func PickFirstThrottleMatch(rules []EdgeRuleThrottleResolved, requestPath, method string) *EdgeRuleThrottleResolved {
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
