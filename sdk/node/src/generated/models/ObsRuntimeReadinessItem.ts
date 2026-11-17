/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original readiness and active reason sample clocks; ready requires a fresh successful scrape and complete consistent one-hot reasons.
 */
export type ObsRuntimeReadinessItem = {
  state: 'ready' | 'not_ready' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason: 'ready' | 'draining' | 'database' | 'vmmd' | 'grpc' | 'storage' | 'credentials' | 'stale' | 'process' | 'other' | 'expected_target_missing' | 'readiness_missing' | 'scrape_stale' | 'readiness_stale' | 'conflicting_targets' | 'conflicting_readiness' | 'source_unavailable' | 'invalid_evidence' | 'invalid_sample_time' | 'scrape_before_boundary' | 'scrape_down' | 'readiness_before_boundary';
  observed_at?: string;
  reason_observed_at?: string;
};

