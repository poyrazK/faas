/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Returned only after the verified event has a committed durable invocation
 * row or, for an operation-bound endpoint, a committed operation and event
 * receipt; receipt_id is then the operation ID. ignored means a verified
 * event outside the operation binding's event_types created no work.
 *
 */
export type InboundWebhookReceiptResponse = {
  receipt_id: string;
  status: 'accepted' | 'ignored';
  /**
   * True when this endpoint already accepted the same provider event ID.
   */
  duplicate: boolean;
  accepted_at: string;
};

