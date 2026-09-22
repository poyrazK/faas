/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InvocationDestinations } from './InvocationDestinations.js';
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Body for POST /v1/apps/{slug}/delayed-tasks. Supply exactly one scheduling form; the maximum delay is 31,536,000 seconds (365 days).
 */
export type DelayedTaskRequest = {
  payload?: Record<string, any>;
  scheduled_at?: string;
  delay_seconds?: number;
  headers?: Record<string, any>;
  method?: string;
  path?: string;
  retry_policy?: RetryPolicyDTO;
  retention_seconds?: number;
  destinations?: InvocationDestinations;
};

