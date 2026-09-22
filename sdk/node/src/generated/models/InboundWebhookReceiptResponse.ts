/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Returned only after the verified event has a committed durable invocation row.
 */
export type InboundWebhookReceiptResponse = {
  receipt_id: string;
  status: 'accepted';
  /**
   * True when this endpoint already accepted the same provider event ID.
   */
  duplicate: boolean;
  accepted_at: string;
};

