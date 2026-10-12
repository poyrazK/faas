/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Unsampled calls for one caller → target pair. `errors` are final 5xx
 * responses after proxy retries; identity and authorization failures
 * are excluded. Latency covers successful calls, including routing,
 * wake, and forwarding time.
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
   * making up at least half of the edge's calls.
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
};

