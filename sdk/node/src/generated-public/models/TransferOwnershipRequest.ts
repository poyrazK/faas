/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/orgs/{slug}/transfer_ownership body. The new owner
 * must already be an active member of the org; the previous
 * owner becomes admin on success. The exactly-one-owner
 * invariant is enforced by the partial unique index
 * `org_memberships_one_owner_idx` (migration 00099).
 *
 */
export type TransferOwnershipRequest = {
  new_owner_account_id: string;
};

