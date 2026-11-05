/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PATCH /v1/orgs/{slug}/members/{user_id} body. Role cannot
 * be `owner` on this endpoint — transfer-ownership is the
 * only path to owner.
 *
 */
export type ChangeMemberRoleRequest = {
  role: 'admin' | 'developer' | 'viewer' | 'billing';
};

