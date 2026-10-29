/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/orgs/{slug}/members body. Role cannot be `owner`.
 * The handler mints a 32-byte plaintext token (returned ONCE
 * in the response) and stores only the SHA-256 hash. The
 * token expires after 14 days.
 *
 */
export type InviteMemberRequest = {
  email: string;
  role: 'admin' | 'developer' | 'viewer' | 'billing';
};

