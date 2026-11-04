/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed sampled CPU/s comparison, optionally with CPU seconds per weighted observed request. exceeds_threshold and relative_increase_percent use the metric selected by options. The relative percentage is absent when that metric's baseline is zero.
 */
export type ProfileRegressionMetric = {
  baseline_cpu_per_second: number;
  candidate_cpu_per_second: number;
  delta_cpu_per_second: number;
  relative_increase_percent?: number;
  /**
   * Present when retained request telemetry has observations for both deployment windows. Counts are weighted collapsed rows with minute-bucket timestamps; boundary counts can be approximate and telemetry can be incomplete.
   */
  cpu_per_request?: {
    baseline_cpu_seconds_per_request: number;
    candidate_cpu_seconds_per_request: number;
    delta_cpu_seconds_per_request: number;
    relative_increase_percent?: number;
    exceeds_threshold: boolean;
  };
  exceeds_threshold: boolean;
};

