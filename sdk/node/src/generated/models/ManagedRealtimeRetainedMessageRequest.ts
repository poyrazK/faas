/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Binary-safe payload to append to retained channel history.
 */
export type ManagedRealtimeRetainedMessageRequest = {
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

