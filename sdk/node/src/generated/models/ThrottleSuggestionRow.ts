/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One (route → suggested rate) row in the payload returned by
 * `GET /v1/apps/{slug}/throttle-suggestions`. The recommender is read-only — it
 * never auto-applies — and the suggestion is always ≤ the
 * customer's plan ceiling so a customer can act on it
 * without a 422 from the API's sub-plan validator.
 *
 */
export type ThrottleSuggestionRow = {
  /**
   * The bounded label exactly as emitted on the Prometheus
   * side: `<METHOD> <PATH>` (pre-edge-rule-rewrite), or
   * the reserved `__route_other__` overflow bucket label.
   *
   */
  route: string;
  /**
   * The `rate()` value over the window (already per-second).
   *
   */
  observed_rps: number;
  /**
   * `ceil(observed_rps * multiplier)` clamped into
   * `[1, plan.RateLimitRPS]`. The 2× headroom is echoed
   * on the wire so the value is auditable rather than
   * magic.
   *
   */
  suggested_rps: number;
  /**
   * `ceil(suggested_rps * 1.5)` clamped into
   * `[1, plan.RateLimitBurst]`. The 1.5× factor is a
   * softer version of the rate headroom — burst oversize
   * is the most common cause of customer-flapping 429s.
   *
   */
  suggested_burst: number;
  /**
   * The headroom factor the recommender applied to
   * `observed_rps`. Pinned on the wire so a future
   * strategy change is distinguishable from drift.
   *
   */
  multiplier: number;
};

