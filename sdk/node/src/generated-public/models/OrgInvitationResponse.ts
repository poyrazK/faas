/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire shape for a single invitation row. Status is the
 * runtime-derived value (pending | consumed | revoked |
 * expired) computed from the (consumed_at, revoked_at,
 * expires_at) tuple. Plaintext token is NEVER re-served —
 * it's returned ONCE on the create call via
 * InvitationWithTokenResponse.
 *
 */
export type OrgInvitationResponse = {
  /**
   * 32-hex opaque invitation id (NOT canonical UUID).
   */
  id: string;
  org_id: string;
  org_slug: string;
  email: string;
  role: 'admin' | 'developer' | 'viewer' | 'billing';
  status: 'pending' | 'consumed' | 'revoked' | 'expired';
  expires_at: string;
  created_at: string;
};

