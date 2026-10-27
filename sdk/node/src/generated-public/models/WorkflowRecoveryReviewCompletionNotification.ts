/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryReviewStatus } from './WorkflowRecoveryReviewStatus.js';
/**
 * Immutable final plan result and completion receiver capture. Queued means a durable event was prepared; HTTP acknowledgement is recorded separately in the delivery ledger.
 */
export type WorkflowRecoveryReviewCompletionNotification = {
  event_id: string;
  status: 'queued' | 'no_receivers';
  captured_at: string;
  receiver_webhook_ids: Array<string>;
  result: WorkflowRecoveryReviewStatus;
};

