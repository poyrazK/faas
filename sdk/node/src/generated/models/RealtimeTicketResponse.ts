/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Ticket Response.
 */
export type RealtimeTicketResponse = {
  /**
   * Opaque bearer credential returned once; store only in client memory.
   */
  ticket: string;
  /**
   * Identifies the ticket for revocation; safe to log and store.
   */
  ticket_id: string;
  expires_at: string;
};

