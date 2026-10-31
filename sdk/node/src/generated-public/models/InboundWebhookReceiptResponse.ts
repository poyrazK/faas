/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Returned only after the verified event has a committed durable invocation
 * row or, for an operation-bound endpoint, a committed operation and event
 * receipt; receipt_id is then the operation ID. ignored means a verified
 * event outside the operation binding's event_types created no work.
 * For a signal-mode binding receipt_id identifies the provider
 * event's signal: signaled means at least one active operation received
 * it, unmatched means none did, and ignored means the type is unmapped
 * or the payload lacks the subject.
 *
 */
export type InboundWebhookReceiptResponse = {
  receipt_id: string;
  status: 'accepted' | 'ignored' | 'signaled' | 'unmatched';
  /**
   * True when this endpoint already accepted the same provider event ID.
   */
  duplicate: boolean;
  accepted_at: string;
};

