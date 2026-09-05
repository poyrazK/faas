/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsTenantApp } from './ObsTenantApp.js';
import type { ObsTenantBilling } from './ObsTenantBilling.js';
import type { ObsTenantCounts } from './ObsTenantCounts.js';
import type { ObsTenantOrg } from './ObsTenantOrg.js';
import type { ObsTenantRow } from './ObsTenantRow.js';
import type { ObsTenantUsage } from './ObsTenantUsage.js';
/**
 * Bounded tenant 360 view - identity, apps, usage, and billing summary.
 */
export type ObsTenant360Response = {
  account: ObsTenantRow;
  apps: Array<ObsTenantApp>;
  orgs: Array<ObsTenantOrg>;
  api_keys: ObsTenantCounts;
  sessions: ObsTenantCounts;
  usage: ObsTenantUsage;
  billing: ObsTenantBilling;
};

