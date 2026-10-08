/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A committed message and its channel-scoped sequence.
 */
export type ManagedRealtimeRetainedMessageResponse = {
  /**
   * Exact-match routing metadata persisted with this channel message.
   */
  metadata?: Record<string, string>;
  sequence: number;
  data_base64: string;
  binary: boolean;
  created_at: string;
  target_message_id?: string;
  version?: number;
  event?: string;
  deleted?: boolean;
};

