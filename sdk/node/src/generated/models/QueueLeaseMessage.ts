/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One leased message. Its receipt acts only while this lease is current.
 */
export type QueueLeaseMessage = {
  id: string;
  /**
   * Opaque; acts only while this lease is current.
   */
  receipt: string;
  payload: Record<string, any>;
  headers?: Record<string, string>;
  attempt: number;
  enqueued_at: string;
  lease_expires_at: string;
};

