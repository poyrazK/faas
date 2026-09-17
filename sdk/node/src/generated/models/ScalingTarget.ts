/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * (metric, value) pair the engine watches for the scale-up trigger. The metric surface is closed; the unset state (null) is the legacy 'engine falls back to autoscale_target_rps' path.
 */
export type ScalingTarget = {
  metric?: 'rps' | 'concurrent_requests' | 'queue_depth' | 'p99_latency_ms';
  /**
   * Target value (units depend on Metric). Must be >= 0; queue_depth requires a positive per-worker backlog budget.
   */
  value?: number;
};

