/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One hourly customer-safe delivery analytics bucket.
 */
export type AppLogDrainAnalyticsBucket = {
  start: string;
  delivered: number;
  failed: number;
  dropped: number;
  retries: number;
  dead_letters: number;
  pending_records: number;
  pending_bytes: number;
  success_rate: number;
  average_latency_ms: number;
};

