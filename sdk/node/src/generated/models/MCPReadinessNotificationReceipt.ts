/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable recovery decision retained transactionally with the queue change.
 */
export type MCPReadinessNotificationReceipt = {
  /**
   * Receiver selected by the retained decision; absent for a whole-event receipt.
   */
  destination?: 'primary' | 'fallback';
  request_id: string;
  event_id: string;
  action: 'retry' | 'abandon';
  reason: string;
  created_at: string;
  created_by: string;
  delivery_version: number;
};

