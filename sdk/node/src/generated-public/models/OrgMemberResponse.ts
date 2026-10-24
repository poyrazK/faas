/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire shape for a single org membership row. Email is the
 * joined account.email; the account_id is also surfaced for
 * non-ambiguous referential purposes (e.g. role-change / remove
 * path segments).
 *
 */
export type OrgMemberResponse = {
  account_id: string;
  email: string;
  role: 'owner' | 'admin' | 'developer' | 'viewer' | 'billing';
  joined_at: string;
};

