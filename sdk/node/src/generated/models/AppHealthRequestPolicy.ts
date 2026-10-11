/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Diagnostic severity defaults, independent of customer SLO configuration. Error thresholds require both minimum request and server-error counts.
 */
export type AppHealthRequestPolicy = {
  minimum_requests: number;
  minimum_server_errors: number;
  warning_error_rate_pct: number;
  unhealthy_error_rate_pct: number;
};

