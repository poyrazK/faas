/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Declarative setup for the common queue worker profile. The server
 * reconciles the default push binding, consumer projection, and
 * queue-depth scaling policy as one idempotent operation.
 *
 */
export type QueueWorkloadProfileRequest = {
  queue_name?: string;
  /**
   * Defaults to the app workload class.
   */
  workload_class?: 'worker' | 'job';
  max_concurrency?: number;
  target_depth?: number;
  /**
   * Optional per-binding retry curve.
   */
  retry_policy?: RetryPolicyDTO;
  /**
   * Replace an existing default binding that points at another queue.
   */
  force?: boolean;
};

