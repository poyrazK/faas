/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Body for POST /v1/apps/{slug}/queues/send. Cap-checked against MaxQueueDepth.
 */
export type QueueSendRequest = {
  payload?: Record<string, any>;
  /**
   * Optional per-message retry curve override.
   */
  retry_policy?: RetryPolicyDTO;
};

