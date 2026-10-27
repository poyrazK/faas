/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregate customer-safe delivery analytics over the requested window.
 */
export type AppLogDrainAnalyticsSummary = {
  delivered: number;
  failed: number;
  dropped: number;
  retries: number;
  dead_letters: number;
  success_rate: number;
  average_latency_ms: number;
};

