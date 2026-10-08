/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ProfileRegressionCPUPerRequestMetric = {
  baseline_cpu_seconds_per_request: number;
  candidate_cpu_seconds_per_request: number;
  delta_cpu_seconds_per_request: number;
  relative_increase_percent?: number;
  exceeds_threshold: boolean;
};

