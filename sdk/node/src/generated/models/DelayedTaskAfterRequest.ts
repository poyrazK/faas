/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InvocationDestinations } from './InvocationDestinations.js';
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Schedule a delayed task after a relative whole-second delay.
 */
export type DelayedTaskAfterRequest = {
  payload?: Record<string, any>;
  delay_seconds: number;
  headers?: Record<string, any>;
  method?: string;
  path?: string;
  retry_policy?: RetryPolicyDTO;
  retention_seconds?: number;
  destinations?: InvocationDestinations;
};

