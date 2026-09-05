/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppErrorSummaryItem } from './AppErrorSummaryItem.js';
import type { AppMetricsResponse } from './AppMetricsResponse.js';
/**
 * Health counters for one app over the requested window.
 */
export type ObsAppHealth = {
  generated_at: string;
  metrics: AppMetricsResponse;
  errors: Array<AppErrorSummaryItem>;
  errors_window_start: string;
  errors_window_end: string;
};

