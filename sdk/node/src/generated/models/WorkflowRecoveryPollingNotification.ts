/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPollingWebhookPayload } from './WorkflowRecoveryPollingWebhookPayload.js';
/**
 * Last captured polling alert receipt. queued means durable outbox admission; no_receivers never replays to later subscribers. Successful polling retains the recovered receipt separately from the cleared error metadata.
 */
export type WorkflowRecoveryPollingNotification = {
  payload: WorkflowRecoveryPollingWebhookPayload;
  status: 'queued' | 'no_receivers';
  captured_at: string;
  receiver_webhook_ids: Array<string>;
};

