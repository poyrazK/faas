/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Queue-driven autoscaling policy for execution_mode='worker'. Supports scale-to-zero when min=0.
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
   * Queue metric driving autoscaling.
   */
  metric: 'queue_lag' | 'queue_depth';
  /**
   * Target backlog per worker instance (e.g. 500 messages per worker).
   */
  target: number;
};

