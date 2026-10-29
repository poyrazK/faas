/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ThrottlePreviewRow } from './ThrottlePreviewRow.js';
import type { ThrottleSuggestionRow } from './ThrottleSuggestionRow.js';
/**
 * Per-route throttle recommendation payload. Source is `prometheus` on success
 * or `degraded: <reason>` on Prometheus failure (response is
 * still 200 with empty Suggestions — the dashboard's
 * empty-state branch handles it).
 *
 * When the request
 * supplies `dry_run=true` + `candidate_rps` + (optional)
 * `candidate_burst`, the response ALSO carries a per-route
 * `would_have_rejected` array + the static
 * `per_consumer_limit_note` literal that names the
 * `gateway_requests_by_route_total` label gap. Dry-run is a
 * guard-rail for the customer's own probe value — not
 * auto-apply.
 *
 */
export type ThrottleSuggestionsResponse = {
  app_id: string;
  range: string;
  /**
   * `prometheus` on success, `degraded: <reason>` on
   * PromQL failure.
   *
   */
  source: string;
  /**
   * RFC 3339 UTC timestamp the response was assembled.
   */
  as_of?: string;
  /**
   * True when `apps.route_metrics_enabled=false` (Free
   * plan). The response carries empty Suggestions plus
   * this flag so the dashboard can render the upsell
   * rather than a misleading zero.
   *
   */
  route_metrics_disabled: boolean;
  /**
   * Count of routes that collapsed into the reserved
   * `__route_other__` overflow bucket during the window
   * (cap = 50). A non-zero value indicates the
   * throttle is partial-coverage regardless of the
   * configured limit.
   *
   */
  routes_collapsed: number;
  /**
   * `plan.RateLimitRPS` — the sub-plan ceiling the
   * suggestion is clamped to. 0 on unknown plans (fail
   * OPEN — the API sub-plan validator is the
   * authoritative gate).
   *
   */
  plan_ceiling_rps: number;
  /**
   * `plan.RateLimitBurst` — the sub-plan ceiling for
   * burst. 0 on unknown plans.
   *
   */
  plan_ceiling_burst: number;
  /**
   * The headroom factor the recommender applied to every
   * route (`Multiplier` constant). Echoed on the wire
   * so the strategy is auditable.
   *
   */
  multiplier: number;
  suggestions: Array<ThrottleSuggestionRow>;
  /**
   * True iff the request supplied `dry_run=true`. The
   * `would_have_rejected` + `per_consumer_limit_note`
   * fields are only populated when this is true.
   *
   */
  dry_run?: boolean;
  /**
   * Echo of the customer's probe value (request
   * `candidate_rps`). Surfaced so a customer reading
   * the wire doesn't have to correlate the preview
   * rows with the request they sent.
   *
   */
  candidate_rps?: number;
  /**
   * Echo of the customer's probe burst (request
   * `candidate_burst`, optional).
   *
   */
  candidate_burst?: number;
  /**
   * One row per surviving route with the count of
   * sub-windows where observed rps exceeded
   * `candidate_rps` over the recommendation window.
   * The preview counts at rule scope — see
   * `per_consumer_limit_note` for the label-gap
   * caveat.
   *
   */
  would_have_rejected?: Array<ThrottlePreviewRow>;
  /**
   * Static literal naming the
   * `gateway_requests_by_route_total` label gap (no
   * per-consumer labels today). Surfaced so dashboards
   * / CLIs reading the preview don't silently
   * mis-attribute a rule-scope count to a
   * per-consumer scope.
   *
   */
  per_consumer_limit_note?: string;
};

