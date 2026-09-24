/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantCredentialResult } from './PlatformTenantCredentialResult.js';
/**
 * Atomic credential plan or applied result for one platform tenant.
 */
export type ApplyPlatformTenantCredentialsResponse = {
  tenant_id: string;
  dry_run: boolean;
  keys: Array<PlatformTenantCredentialResult>;
};

