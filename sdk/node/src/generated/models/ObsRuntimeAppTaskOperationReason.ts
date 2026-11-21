/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Closed source or comparison diagnostic. Previous-source reasons refer to the pinned earlier read. Changed process, repeated samples, resets and clock mismatches establish no delta.
 */
export type ObsRuntimeAppTaskOperationReason = 'source_unavailable' | 'invalid_evidence' | 'invalid_sample_time' | 'scrape_before_boundary' | 'scrape_down' | 'metric_before_boundary' | 'invalid_value' | 'expected_target_missing' | 'conflicting_targets' | 'metric_missing' | 'metric_stale' | 'conflicting_series' | 'conflicting_sample_times' | 'scrape_stale' | 'previous_source_unavailable' | 'previous_invalid_evidence' | 'previous_invalid_sample_time' | 'previous_scrape_before_boundary' | 'previous_scrape_down' | 'previous_metric_before_boundary' | 'previous_invalid_value' | 'previous_expected_target_missing' | 'previous_conflicting_targets' | 'previous_metric_missing' | 'previous_metric_stale' | 'previous_conflicting_series' | 'previous_conflicting_sample_times' | 'previous_scrape_stale' | 'process_evidence_unavailable' | 'process_changed' | 'new_target' | 'repeated_process_sample' | 'process_comparison_unavailable' | 'sample_clock_mismatch' | 'process_window_uncertain' | 'counter_reset';
