/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantCredentialMetadata } from './PlatformTenantCredentialMetadata.js';
/**
 * Credential metadata only; no plaintext key, including on creation.
 */
export type PlatformTenantCredentialResult = (PlatformTenantCredentialMetadata & {
  action: 'create' | 'revoke' | 'unchanged';
});

