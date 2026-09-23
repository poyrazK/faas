/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InvocationDestinations } from './InvocationDestinations.js';
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Schedule a delayed task at an absolute RFC 3339 timestamp.
 */
export type DelayedTaskAtRequest = {
  payload?: Record<string, any>;
  scheduled_at: string;
  headers?: Record<string, any>;
  method?: string;
  path?: string;
  retry_policy?: RetryPolicyDTO;
  retention_seconds?: number;
  destinations?: InvocationDestinations;
};

