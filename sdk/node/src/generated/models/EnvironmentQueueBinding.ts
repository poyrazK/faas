/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Logical queue consumer intent for a workload.
 */
export type EnvironmentQueueBinding = {
  queue_name: string;
  mode?: 'pull' | 'push';
  workload_class: string;
  enabled?: boolean;
  max_concurrency?: number;
  retry_policy?: RetryPolicyDTO;
};

