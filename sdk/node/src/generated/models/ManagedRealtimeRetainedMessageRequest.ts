/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Binary-safe payload to append to retained channel history.
 */
export type ManagedRealtimeRetainedMessageRequest = {
  /**
   * Optional channel head sequence required before this retained publish; zero requires an empty channel.
   */
  expected_sequence?: number | null;
  /**
   * Routing metadata attached to this retained publish; at most 4096 encoded JSON bytes.
   */
  metadata?: Record<string, string>;
  /**
   * Standard base64 for at most 4096 decoded bytes.
   */
  data_base64: string;
  binary?: boolean;
  /**
   * Deduplicates identical writes while retained.
   */
  idempotency_key?: string;
};

