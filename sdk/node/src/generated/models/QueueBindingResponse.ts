/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Durable queue-to-workload binding.
 */
export type QueueBindingResponse = {
  id: string;
  app_id: string;
  account_id: string;
  name: string;
  queue_name: string;
  mode: 'pull' | 'push';
  workload_class: 'worker' | 'job';
  enabled: boolean;
  max_concurrency: number;
  retry_policy?: RetryPolicyDTO;
  created_at: string;
  updated_at: string;
};

