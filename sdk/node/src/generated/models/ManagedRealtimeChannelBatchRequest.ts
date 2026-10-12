/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeBatchMessageRequest } from './ManagedRealtimeBatchMessageRequest.js';
/**
 * Atomic batch of retained channel messages with an optional sequence precondition.
 */
export type ManagedRealtimeChannelBatchRequest = {
  /**
   * Default delivery lifetime for all batch messages; zero disables expiry.
   */
  ttl_seconds?: number;
  /**
   * Precondition for the channel head before the whole batch.
   */
  expected_sequence?: number | null;
  batch_id: string;
  messages: Array<ManagedRealtimeBatchMessageRequest>;
};

