/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OrgInvitationResponse } from './OrgInvitationResponse.js';
/**
 * POST /v1/orgs/{slug}/members response. Carries the
 * one-time plaintext token in addition to the canonical
 * invitation shape. Never re-served on subsequent reads —
 * losing the token means revoking the invitation and
 * inviting again.
 *
 */
export type InvitationWithTokenResponse = (OrgInvitationResponse & {
  /**
   * 32-byte plaintext token, base64url-encoded (44 chars).
   * Returned ONCE; never re-served. SHA-256 hash is what's
   * stored. Treat as a secret — anyone with this token
   * can accept the invitation.
   *
   */
  token: string;
});

