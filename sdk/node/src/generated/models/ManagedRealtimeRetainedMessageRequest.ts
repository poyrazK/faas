/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Binary-safe payload to append to retained channel history.
 */
export type ManagedRealtimeRetainedMessageRequest = {
  /**
   * Optional current channel sequence precondition; zero requires an empty channel. Retained channel writes only.
   */
  expected_sequence?: number | null;
  /**
   * Exact-match routing metadata; at most 4096 encoded JSON bytes. Retained channel publishing only.
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

