/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Queue-driven or custom-metric autoscaling policy for execution_mode='worker'. Supports scale-to-zero when min=0.
 */
export type WorkerScaling = {
  /**
   * Minimum worker instances to maintain. 0 enables scale-to-zero.
   */
  min: number;
  /**
   * Maximum worker instances (bounded by plan WorkerReplicasMax and app max_concurrency).
   */
  max: number;
  /**
   * Queue or customer-pushed metric driving autoscaling.
   */
  metric: 'queue_lag' | 'queue_depth' | 'custom';
  /**
   * Required when metric is custom; the app-scoped custom gauge name.
   */
  name?: string;
  /**
   * Target backlog per worker instance. Custom metric targets use the custom gauge's units.
   */
  target: number;
};
