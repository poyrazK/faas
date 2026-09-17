/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Partial queue-binding update; omitted fields are unchanged.
 */
export type UpdateQueueBindingRequest = {
  queue_name?: string;
  mode?: 'pull' | 'push';
  workload_class?: 'worker' | 'job';
  enabled?: boolean;
  max_concurrency?: number;
  retry_policy?: RetryPolicyDTO;
};

