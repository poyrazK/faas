/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { QueueSendBatchMessage } from './QueueSendBatchMessage.js';
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Body for POST /v1/apps/{slug}/queues/send-batch: up to 100 messages for one queue, sent in order. The body is at most 8 MiB; each message meets its plan's message size.
 */
export type QueueSendBatchRequest = {
  /**
   * Target registered environment with an enabled scoped queue binding. Omission sends through the default environment.
   */
  environment?: string;
  /**
   * Optional logical queue name. Required when an app has multiple enabled queue consumers.
   */
  queue_name?: string;
  /**
   * Optional retry curve override for every message of the batch.
   */
  retry_policy?: RetryPolicyDTO;
  messages: Array<QueueSendBatchMessage>;
};

