/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReceiptRecipientResponse } from './EventReceiptRecipientResponse.js';
/**
 * Acceptance and routing evidence with a bounded recipient page; execution is a separate lifecycle.
 */
export type EventReceiptResponse = {
  event_id: string;
  event_source: string;
  event_type: string;
  schema_version?: string;
  /**
   * First durable acceptance time, preserved on identical retries.
   */
  accepted_at: string;
  /**
   * All routing candidates settled, including filtered and failed outcomes; absent while routing is active.
   */
  routing_settled_at?: string;
  /**
   * Settled receipt retention boundary, 30 days after routing settled. Absent for active routing.
   */
  retain_until?: string;
  /**
   * False means legacy recipient membership is unknown, rather than an empty matching set.
   */
  snapshot_captured: boolean;
  /**
   * Whole-event legacy routing or independent recipient leases.
   */
  routing_mode: 'event' | 'recipient';
  /**
   * Total captured candidates across all pages; unknown when snapshot_captured=false.
   */
  recipient_count: number;
  /**
   * Counts across all captured recipients by current routing checkpoint state.
   */
  routing_summary: Record<string, number>;
  recipients: Array<EventReceiptRecipientResponse>;
  /**
   * Opaque cursor for the next acceptance-ordered recipient page.
   */
  next_after?: string;
};

