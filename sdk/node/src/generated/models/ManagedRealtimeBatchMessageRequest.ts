/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One retained publication in a channel batch, with an optional override of the batch message TTL.
 */
export type ManagedRealtimeBatchMessageRequest = {
  /**
   * Override the batch TTL; omitted or null inherits the default and zero disables expiry for this message.
   */
  ttl_seconds?: number | null;
  data_base64: string;
  metadata?: Record<string, string>;
  binary?: boolean;
  /**
   * Per-message preconditions are unsupported. Omit this field or set it to null; use the batch-level expected_sequence to require a channel head.
   */
  expected_sequence?: null;
};

