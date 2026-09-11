/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-route token-bucket cap (ADR-091 D20.5 amendment,
 * issue #881). Customers tighten the per-route rps/burst
 * below their plan's `plan.RateLimitRPS` — the apid
 * validator enforces the sub-plan ceiling; the gateway
 * compiler enforces it again at load time.
 *
 * Sub-plan ceiling — the load-bearing constraint. A
 * throttle rule is STRICTLY a tightening primitive. A
 * rule that exceeds the ceiling is rejected with 422
 * BEFORE any DB write — a customer cannot raise their
 * plan limit by registering a throttle rule.
 *
 * Per-IP sub-keying is deliberately absent in v1 — see
 * the package doc on `pkg/state.EdgeRuleThrottleAction`
 * for the design rationale (memory-bounded limiter +
 * attacker-controlled IP cardinality = unbounded bucket
 * growth).
 *
 * Phase 3 (ADR-091 D20.5 amendment 4, ADR-104, issue #881
 * Phase 3) extends the wire shape with optional per-consumer
 * keying. Default values (`""` for key_by, omitted
 * jwt_claim_name, 0 for max_keys_per_rule) preserve PR
 * #887's behaviour bit-for-bit. See ADR-104 for the policy
 * and `pkg/gateway/ratelimit.go::AllowWithConsumerKey`
 * (Phase 3) for the run-time semantics.
 *
 */
export type EdgeRuleThrottleAction = {
  /**
   * Token-bucket refill rate per route. The apid
   * validator rejects rps > plan.RateLimitRPS with a
   * 422. The gateway compiler clamps + warns on the
   * same bound at load time.
   *
   */
  requests_per_second: number;
  /**
   * Token-bucket burst per route. Mirrors rps: rejected
   * above `plan.RateLimitBurst` at create time and
   * clamped at compile time.
   *
   */
  burst: number;
  /**
   * Per-consumer keying dimension (ADR-104 / issue #881
   * Phase 3). When `""` or `"none"`, the bucket is shared
   * across every caller of the route (PR #887 shape).
   * When `"api_key"`, one bucket per authenticated API
   * key. When `"consumer_id"`, one bucket per stable API consumer
   * identity (all rotated keys for that consumer share a bucket).
   * When `"jwt_subject"`, one bucket per JWT `sub`.
   * When `"jwt_claim"`, one bucket per value of the
   * claim named by `jwt_claim_name`. Each non-empty
   * value activates the bounded design: when the
   * per-rule consumer set exceeds
   * `max_keys_per_rule`, all over-cap callers collapse
   * into a single non-evicting `__other__` bucket that
   * still consumes tokens (the load-bearing safety
   * property — see ADR-104 §"Consequences").
   *
   */
  key_by?: '' | 'none' | 'api_key' | 'consumer_id' | 'jwt_subject' | 'jwt_claim';
  /**
   * Required iff `key_by="jwt_claim"`. Names the JWT
   * custom claim to extract (e.g., `"tier"`,
   * `"org_id"`). Format is a CodeQL safe-identifier:
   * leading letter or underscore, then `[A-Za-z0-9_]`,
   * max 64 chars. Anything looser risks label-cardinality
   * explosion in metric series or a CodeQL go-clear-
   * text-logging finding on a future refactor.
   *
   */
  jwt_claim_name?: string;
  /**
   * Caps the cardinality of the per-consumer bucket map
   * for this rule. 0 means "use plan default"
   * (`Limits.ThrottleMaxKeysPerRule` per plan: Free 100
   * / Hobby 1000 / Pro 5000 / Scale 10000). The
   * validator rejects values above
   * `plan.ThrottleMaxKeysPerRule`. Must be 0 when
   * `key_by` is `""` or `"none"` — the cap is moot for
   * non-per-consumer rules.
   *
   */
  max_keys_per_rule?: number;
};

