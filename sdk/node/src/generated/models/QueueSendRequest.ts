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
   * Registered project environment. Requires an enabled environment-owned queue binding; omitted uses the default environment and legacy shared queues.
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
   * Optional application-keyed policy. Environment-owned queue bindings require a scoped policy adapter and currently reject this option.
   */
  work?: InvokeWork;
};

