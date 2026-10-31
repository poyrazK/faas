/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIKeyResponse } from './APIKeyResponse.js';
/**
 * Response body of POST /v1/keys/{id}/rotate. The new `key_plaintext` is returned exactly once; the old plaintext is NEVER returned (only the hash is stored). `old_key_expires_at` is the grace deadline applied to the predecessor — the customer's CI rotates over by then.
 */
export type RotateKeyResponse = {
  key: APIKeyResponse;
  /**
   * New plaintext (PRESENT ONLY on this response). Capture immediately; the API never returns it again.
   */
  key_plaintext: string;
  /**
   * Predecessor key id (matches Key.rotated_from_id).
   */
  old_key_id: string;
  /**
   * Grace deadline applied to the old key (RFC 3339, UTC). When grace_window_days=0 the deadline is 'now' (atomic rotation).
   */
  old_key_expires_at: string;
};

