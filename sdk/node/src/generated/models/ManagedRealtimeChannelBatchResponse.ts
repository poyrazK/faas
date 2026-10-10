/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimePublishResponse } from './ManagedRealtimePublishResponse.js';
/**
 * Committed retained batch and per-message live fanout outcomes.
 */
export type ManagedRealtimeChannelBatchResponse = {
  batch_id: string;
  durable: boolean;
  partial: boolean;
  messages: Array<ManagedRealtimePublishResponse>;
};

