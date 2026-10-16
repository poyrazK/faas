/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete cohort partition and valid-sample percentiles without substituting zeros for missing evidence.
 */
export type ObsAlertResponseMetric = {
  samples: number;
  not_observed: number;
  unknown: number;
  invalid_clock: number;
  p50_seconds: number | null;
  p95_seconds: number | null;
};

