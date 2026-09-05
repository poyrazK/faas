/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One tenant in the operator inventory.
 */
export type ObsTenantRow = {
  account_id: string;
  plan: string;
  status: string;
  org_slug?: string;
  is_personal: boolean;
  created_at: string;
  mfa_enrolled: boolean;
  apps_count: number;
  deployments_live_count: number;
  email?: string;
};

