/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InvokeWork } from './InvokeWork.js';
import type { RetryPolicyDTO } from './RetryPolicyDTO.js';
/**
 * Body for POST /v1/apps/{slug}/queues/send. Cap-checked against MaxQueueDepth. Unkeyed messages retain legacy FIFO dispatch; keyed messages use per-key ordering.
 */
export type QueueSendRequest = {
  /**
   * Target registered environment with an enabled scoped queue binding. Omission sends through the default environment and legacy shared queues.
   */
  environment?: string;
  payload?: Record<string, any>;
  /**
   * Optional bounded Gregale Flags context produced by the Node SDK from decisions marked used. The platform validates the envelope, binds it to its active customer, and restores it on the queued request.
   */
  flag_context?: string;
  /**
   * Optional logical queue name. Required when an app has multiple enabled queue consumers.
   */
  queue_name?: string;
  /**
   * Optional per-message retry curve override.
   */
  retry_policy?: RetryPolicyDTO;
  /**
   * Optional keyed ordering policy for a queue message. Scoped environment bindings reject this option until their policy adapter is available.
   */
  work?: InvokeWork;
};

