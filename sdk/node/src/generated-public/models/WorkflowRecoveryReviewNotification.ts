/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One durable review notification capture. Delivery outcomes are in the webhook delivery ledger; queued capture does not prove HTTP acknowledgement. Frozen receiver identifiers prevent replay to newly configured subscriptions.
 */
export type WorkflowRecoveryReviewNotification = {
  event_id: string;
  status: 'queued' | 'no_receivers' | 'suppressed';
  reason: string;
  captured_at: string;
  receiver_webhook_ids: Array<string>;
};

