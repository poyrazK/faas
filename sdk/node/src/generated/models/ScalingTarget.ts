/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * (metric, value) pair the engine watches for the scale-up trigger. The metric surface is closed, and since ADR-194 every member of it is backed by a live source and read by a scheduler trigger. `p99_latency_ms` was removed: it validated for releases with no latency source behind it, and is now rejected with 422 pointing at concurrent_requests. The unset state (null) is the legacy 'engine falls back to autoscale_target_rps' path.
 */
export type ScalingTarget = {
  /**
   * rps = per-instance requests/second. cpu = max per-instance CPU percent. concurrent_requests = max per-instance in-flight requests. queue_depth = fleet backlog budget per worker.
   */
  metric?: 'rps' | 'cpu' | 'concurrent_requests' | 'queue_depth';
  /**
   * Target value (units depend on Metric). Must be >= 0 in the singular `target` field for compatibility; inside `targets` it must be > 0. queue_depth requires a positive per-worker backlog budget, and cpu is capped at 100.
   */
  value?: number;
};

