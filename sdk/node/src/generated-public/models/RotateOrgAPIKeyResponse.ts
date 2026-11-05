/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIKeyResponse } from './APIKeyResponse.js';
/**
 * Response body of POST /v1/orgs/{slug}/keys/{id}/rotate. Mirrors `RotateKeyResponse`; org-scoped because the request was — both keys carry the same `org_id` in `APIKeyResponse.org_id`. The new `key_plaintext` is returned exactly once; the old plaintext is NEVER returned.
 */
export type RotateOrgAPIKeyResponse = {
  key: APIKeyResponse;
  /**
   * New plaintext (PRESENT ONLY on this response). Capture immediately; the API never returns it again. Org-scoped mint; the plaintext belongs to the active org's id.
   */
  key_plaintext: string;
  /**
   * Predecessor key id (matches Key.rotated_from_id). Same org as the new key (rotation is org-local, never re-bound).
   */
  old_key_id: string;
  /**
   * Grace deadline applied to the old key (RFC 3339, UTC). When grace_window_days=0 the deadline is 'now' (atomic rotation). Mirrors the legacy `RotateKeyResponse.old_key_expires_at` contract; the org binding carries through in `APIKeyResponse.org_id`.
   */
  old_key_expires_at: string;
};

