/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata and SHA-256 digest of a locally generated ck_ credential; never submit plaintext.
 */
export type PlatformTenantCredentialIntent = {
  consumer_id: string;
  name: string;
  prefix: string;
  /**
   * Hex-encoded SHA-256 of the entire plaintext key.
   */
  hash: string;
  scopes: Array<'read' | 'write' | 'admin'>;
  expires_at?: string;
};

