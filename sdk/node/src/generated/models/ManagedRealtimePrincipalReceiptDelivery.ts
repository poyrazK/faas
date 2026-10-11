/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-connection receipt state for a principal message dispatch.
 */
export type ManagedRealtimePrincipalReceiptDelivery = {
  connection_id: string;
  status: string;
  queue_status: string;
  ack_supported: boolean;
  created_at: string;
  queued_at?: string;
  acknowledged_at?: string;
};

