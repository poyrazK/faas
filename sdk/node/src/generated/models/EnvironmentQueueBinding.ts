/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Atomic queue binding intent in the named environment. Reviewed adoption preserves the original binding and consumer IDs and delivery history. Queue pruning and retired identity recovery require a separate reviewed disposition.
 */
export type EnvironmentQueueBinding = {
  queue_name: string;
  mode?: 'pull' | 'push';
  /**
   * worker or job, or http for push-only bindings on function workloads
   */
  workload_class: string;
  enabled?: boolean;
  max_concurrency?: number;
  retry_policy?: RetryPolicyDTO;
};

