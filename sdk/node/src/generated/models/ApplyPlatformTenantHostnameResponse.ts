/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Hostname reconciliation action and TXT challenge; new dry-run hostnames have no usable challenge token.
 */
export type ApplyPlatformTenantHostnameResponse = {
  hostname: string;
  challenge_token?: string;
  verified: boolean;
  verified_at?: string;
  last_error?: string;
  txt_record?: string;
  action: 'create' | 'unchanged';
};

