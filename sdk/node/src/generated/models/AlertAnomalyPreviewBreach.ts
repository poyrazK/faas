/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One preview checkpoint that met the anomaly comparison and minimum impact requirement.
 */
export type AlertAnomalyPreviewBreach = {
  evaluated_at: string;
  /**
   * Observed request count, error rate in percent, or p95 latency in milliseconds.
   */
  observed: number;
  /**
   * Median metric value of qualifying historical windows at the same time of day.
   */
  baseline: number;
  /**
   * Baseline-derived trigger or the absolute rate threshold for a zero-baseline fallback.
   */
  trigger_value: number;
  /**
   * Eligible request count for an error-rate or p95 latency check. For p95 this counts successful requests only.
   */
  eligible_requests?: number;
  /**
   * Failed requests for a zero-baseline fallback breach.
   */
  failed_requests?: number;
  /**
   * True when the absolute fallback threshold triggered this breach.
   */
  zero_baseline_fallback?: boolean;
};

