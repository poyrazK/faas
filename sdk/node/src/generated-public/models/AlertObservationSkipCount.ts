/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Count of observation checks skipped for a single reason.
 */
export type AlertObservationSkipCount = {
  reason: 'insufficient_traffic' | 'insufficient_history' | 'telemetry_missing_or_stale' | 'telemetry_unavailable';
  count: number;
};

