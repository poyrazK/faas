/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Binary-safe message payload encoded as standard base64.
 */
export type ManagedRealtimeMessageRequest = {
  /**
   * Optional retained channel delivery lifetime from first commit. Zero disables expiry; retries preserve the original deadline.
   */
  ttl_seconds?: number;
  /**
   * Optional current channel sequence precondition; zero requires an empty channel. Retained channel writes only.
   */
  expected_sequence?: number | null;
  /**
   * Exact-match routing metadata; at most 4096 encoded JSON bytes. Retained channel publishing only.
   */
  metadata?: Record<string, string>;
  /**
   * Decoded payload is limited to 1 MiB.
   */
  data_base64: string;
  binary?: boolean;
};

