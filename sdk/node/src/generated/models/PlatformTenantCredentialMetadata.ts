/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata for a linked consumer key; plaintext and hash are excluded. ID and creation time are omitted for planned keys in a dry run.
 */
export type PlatformTenantCredentialMetadata = {
  id?: string;
  consumer_id: string;
  name: string;
  prefix: string;
  scopes: Array<'read' | 'write' | 'admin'>;
  created_at?: string;
  expires_at?: string;
  last_used_at?: string;
  revoked_at?: string;
};

