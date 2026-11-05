/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Threshold policy for sampled CPU per wall-clock second or per weighted observed request. Both relative and selected-metric absolute increase thresholds must be met. CPU-per-request mode also requires retained request telemetry and its configured minimum request count in both deployment windows; absent or sparse counts are inconclusive. Request telemetry rows use minute-bucket timestamps, so counts near window boundaries can be approximate and may be incomplete. Capture requirements apply separately to each profile window.
 */
export type ProfileRegressionOptions = {
  /**
   * Optional advisory route CPU/request checks using the relative and CPU/request thresholds and minimum requests, regardless of the aggregate metric. Never gates rollout.
   */
  routes?: Array<string>;
  metric?: 'cpu_per_second' | 'cpu_per_request';
  relative_increase_percent: number;
  /**
   * Absolute increase threshold for cpu_per_second mode.
   */
  absolute_increase_cpu_per_second: number;
  /**
   * Required when metric is cpu_per_request.
   */
  absolute_increase_cpu_seconds_per_request?: number;
  minimum_profiles: number;
  minimum_coverage_ratio: number;
  /**
   * Required when metric is cpu_per_request; applied to each deployment window.
   */
  minimum_requests?: number;
};

