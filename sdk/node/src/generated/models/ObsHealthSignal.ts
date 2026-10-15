/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Evidence for one Prometheus-derived health value. Freshness uses the
 * oldest last-observed counter sample or faas.rules.yml rule-group
 * evaluation across series, never the aggregate query's output timestamp.
 * Historical observations are searched with a bounded 15-minute range.
 * Removed series/groups can conservatively hold a signal stale until
 * that history expires. A healthy state describes fresh query evidence,
 * not whether the metric's value indicates an operational problem.
 *
 */
export type ObsHealthSignal = {
  /**
   * Healthy means a valid value and an observation no older than
   * max_age_seconds. No_samples means a missing observation or too
   * few range samples. Stale means a known observation is too old.
   * Unavailable means missing configuration, a failed query, or invalid
   * upstream values. No last-good values are cached across requests.
   *
   */
  state: 'healthy' | 'no_samples' | 'stale' | 'unavailable';
  /**
   * Underlying observation time (UTC), when known; retained for stale evidence.
   */
  observed_at?: string;
  /**
   * Aggregation window; 300 for audit counters, 0 for the instantaneous alert count.
   */
  window_seconds: number;
  /**
   * Freshness threshold; currently 120 seconds.
   */
  max_age_seconds: number;
  /**
   * Closed reason code for non-healthy signals; never raw upstream errors.
   */
  reason?: 'not_configured' | 'query_failed' | 'missing_observation' | 'insufficient_samples' | 'observation_stale' | 'invalid_observation' | 'observation_in_future' | 'invalid_value';
};

