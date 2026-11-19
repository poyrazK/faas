/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sanitized loop observation with original scrape and metric clocks; loop names are selected explicitly per daemon. Age is diagnostic and the producer owns its stall budget.
 */
export type ObsRuntimeLoopItem = {
  job: string;
  daemon: string;
  loop: 'runtime' | 'main' | 'sweep';
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  state: 'no_reported_stall' | 'stalled' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: 'expected_target_missing' | 'source_unavailable' | 'invalid_evidence' | 'conflicting_targets' | 'conflicting_loop_series' | 'invalid_sample_time' | 'scrape_stale' | 'scrape_before_boundary' | 'scrape_down' | 'loop_stall_missing' | 'loop_age_missing' | 'loop_sample_stale' | 'loop_sample_before_boundary' | 'invalid_stall_value' | 'invalid_loop_age';
  reported_stalled?: boolean;
  age_seconds?: number;
  scrape_observed_at?: string;
  stall_observed_at?: string;
  age_observed_at?: string;
};

