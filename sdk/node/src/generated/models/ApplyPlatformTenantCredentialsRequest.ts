/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantCredentialIntent } from './PlatformTenantCredentialIntent.js';
/**
 * Additive key reconciliation and revocation in one transaction; provide at least one key or revocation.
 */
export type ApplyPlatformTenantCredentialsRequest = {
  dry_run?: boolean;
  keys?: Array<PlatformTenantCredentialIntent>;
  revoke_key_ids?: Array<string>;
};

