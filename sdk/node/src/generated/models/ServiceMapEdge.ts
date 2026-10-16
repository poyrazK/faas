/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Unsampled calls for one caller → target pair. `errors` are final 5xx
 * responses after proxy retries; identity and authorization failures
 * are excluded. Latency covers successful calls, including routing,
 * wake, and forwarding time. Private TCP sessions on the same pair are
 * reported in the `tcp_*` fields; a pair with only TCP traffic has zero
 * `calls` and zero latency.
 *
 */
export type ServiceMapEdge = {
  caller_app_id: string;
  caller_app_slug: string;
  target_app_id: string;
  target_app_slug: string;
  calls: number;
  errors: number;
  error_rate_pct: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  /**
   * Calls that found the target parked and waited for it to restore (ADR-196).
   */
  cold_calls: number;
  /**
   * `cold_calls` as a percentage of `calls`.
   */
  cold_call_pct: number;
  /**
   * The caller has not opted into wake-ahead (ADR-977) and its calls
   * mostly wake the target: at least 20 cold calls in the window,
   * making up at least half of the edge's calls. Private TCP sessions
   * count as calls (ADR-1134).
   *
   */
  suggest_wake_ahead: boolean;
  /**
   * Wake-aheads (ADR-977) the caller's wakes started for this target in the window.
   */
  wake_ahead_started: number;
  /**
   * Of those, the wake-aheads whose target was called while they were fresh.
   */
  wake_ahead_used: number;
  /**
   * Private TCP sessions (ADR-576) from the caller to the target in the window, excluding identity, authorization and plan session-limit refusals.
   */
  tcp_sessions: number;
  /**
   * Of those, the sessions that failed after authorization (no replica, wake failure, unreachable guest, or an error while connected).
   */
  tcp_errors: number;
  /**
   * Of those, the sessions that found the target parked and waited for it to restore.
   */
  tcp_cold_sessions: number;
};

