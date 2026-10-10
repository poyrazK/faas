/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimePrincipalReceiptDelivery } from './ManagedRealtimePrincipalReceiptDelivery.js';
/**
 * Aggregate principal receipt and per-connection delivery states.
 */
export type ManagedRealtimePrincipalReceiptResponse = {
  endpoint_id: string;
  message_id: string;
  status: string;
  dispatch_complete: boolean;
  created_at: string;
  expires_at: string;
  recipients: number;
  queued: number;
  acknowledged: number;
  pending: number;
  timed_out: number;
  unsupported: number;
  queue_full: number;
  failed: number;
  nodes_queried: number;
  nodes_unavailable: number;
  partial: boolean;
  deliveries: Array<ManagedRealtimePrincipalReceiptDelivery>;
};

