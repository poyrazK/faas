/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Durable mapping from a logical queue to a worker/job workload.
 */
export type CreateQueueBindingRequest = {
  name: string;
  queue_name: string;
  mode?: 'pull' | 'push';
  workload_class?: 'worker' | 'job';
  enabled?: boolean;
  max_concurrency?: number;
  /**
   * Optional per-binding retry curve; zero values use platform defaults.
   */
  retry_policy?: RetryPolicyDTO;
};

