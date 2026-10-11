/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeInboxMessageResponse } from './ManagedRealtimeInboxMessageResponse.js';
/**
 * Principal inbox page with retention bounds and consumer checkpoint.
 */
export type ManagedRealtimeInboxResponse = {
  messages: Array<ManagedRealtimeInboxMessageResponse>;
  oldest_sequence: number;
  latest_sequence: number;
  history_unavailable: boolean;
  has_more: boolean;
  consumer?: string;
  acknowledged_sequence?: (number | null);
};

