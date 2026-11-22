/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Baseline comparison captured at the time of an anomaly alert fire.
 */
export type AlertDeliveryAnomalyEvidence = {
  metric: 'error_rate_pct' | 'latency_p95_ms' | 'latency_p99_ms' | 'cold_start_pct' | 'request_count';
  comparison: 'above_baseline' | 'below_baseline';
  /**
   * Configured multiplier when the alert fired.
   */
  threshold: number;
  window: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * Median value in the metric's units.
   */
  baseline: number;
  /**
   * Historical windows used in the baseline.
   */
  baseline_days: number;
  /**
   * Observed value divided by baseline.
   */
  ratio: number;
};

