/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A historical checkpoint the anomaly evaluator could not use.
 */
export type AlertAnomalyPreviewSkippedWindow = {
  evaluated_at: string;
  reason: 'telemetry_missing_or_stale' | 'insufficient_history' | 'insufficient_traffic';
  /**
   * Eligible request count when error-rate or p95 latency telemetry is covered. For p95 this counts successful requests only.
   */
  eligible_requests?: number;
  /**
   * Minimum eligible request count for a check skipped for insufficient traffic.
   */
  required_requests?: number;
};

