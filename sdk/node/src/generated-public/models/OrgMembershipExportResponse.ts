/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Organization membership lifecycle row included in export schema v2.
 */
export type OrgMembershipExportResponse = {
  org_id: string;
  org_slug: string;
  account_id: string;
  email: string;
  role: 'owner' | 'admin' | 'developer' | 'billing' | 'viewer';
  invited_by_account_id?: string;
  joined_at: string;
  removed_at?: string;
};

