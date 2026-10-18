/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Episode partition and valid activation-to-resolution observation statistics.
 */
export type ObsAlertRecurrenceDuration = {
  samples: number;
  not_observed: number;
  invalid_clock: number;
  min_seconds: number | null;
  p50_seconds: number | null;
};

