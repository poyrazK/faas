/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeRetainedMessageResponse } from './ManagedRealtimeRetainedMessageResponse.js';
/**
 * A consistent page of retained channel messages and retention bounds.
 */
export type ManagedRealtimeRetainedHistoryResponse = {
  messages: Array<ManagedRealtimeRetainedMessageResponse>;
  oldest_sequence: number;
  latest_sequence: number;
  has_more: boolean;
};

