/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventBacklogRecipient } from './EventBacklogRecipient.js';
import type { EventRecipientBlockerQuery } from './EventRecipientBlockerQuery.js';
/**
 * Current retained routing evidence for one acceptance-guarded consumer query.
 */
export type EventRecipientBlockerResponse = {
  query: EventRecipientBlockerQuery;
  observed_at: string;
  status: 'available' | 'not_waiting' | 'unavailable' | 'acceptance_replaced';
  reason?: 'receipt_unavailable' | 'snapshot_unavailable' | 'recipient_unavailable' | 'routing_evidence_unavailable' | 'acceptance_replaced';
  /**
   * Retained routing state; never an invocation state.
   */
  routing_state?: string;
  recipient?: EventBacklogRecipient;
};

