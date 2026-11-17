/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original uptime and sample clocks; comparison uses the same complete target identity and permits clock skew. Optional fields are absent for unavailable evidence.
 */
export type ObsRuntimeProcessItem = {
  state: 'fresh' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: 'source_unavailable' | 'invalid_evidence' | 'invalid_sample_time' | 'scrape_stale' | 'scrape_before_boundary' | 'scrape_down' | 'expected_target_missing' | 'conflicting_targets' | 'uptime_missing' | 'conflicting_uptime' | 'uptime_stale' | 'uptime_before_boundary' | 'invalid_uptime';
  uptime_seconds?: number;
  observed_at?: string;
  comparison?: 'baseline' | 'changed' | 'unchanged' | 'new_target' | 'repeated_sample' | 'unavailable';
  comparison_reason?: 'unexpected_target' | 'previous_evidence_unavailable' | 'sample_clock_regressed' | 'start_estimate_regressed';
  previous_uptime_seconds?: number;
  previous_observed_at?: string;
  revision_relation?: 'same' | 'different' | 'unknown';
};

