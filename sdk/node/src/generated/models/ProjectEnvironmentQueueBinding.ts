/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Desired logical queue binding without runtime consumer identity or messages. HTTP consumers require push mode and a function workload.
 */
export type ProjectEnvironmentQueueBinding = {
  name: string;
  queue_name: string;
  mode: 'pull' | 'push';
  workload_class: 'worker' | 'job' | 'http';
  enabled: boolean;
  max_concurrency: number;
  retry_policy?: RetryPolicyDTO;
};

