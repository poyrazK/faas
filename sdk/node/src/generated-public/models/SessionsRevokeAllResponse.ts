/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for `POST /v1/auth/sessions/revoke_all`.
 */
export type SessionsRevokeAllResponse = {
  /**
   * Number of sibling sessions revoked. The caller's session is NOT included in this count.
   */
  revoked: number;
};

