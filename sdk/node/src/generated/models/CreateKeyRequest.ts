/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * API key creation payload — label and optional scopes. Plaintext is returned exactly once in the 201 response. Scopes defaults to `["admin"]` when omitted so existing callers preserve the legacy full-access behavior. See IAM-1, ADR-034 rev2.
 */
export type CreateKeyRequest = {
  label?: string;
  /**
   * Requested permission set. The server rejects unknown scopes. Object-storage read/write scopes do not expose data until a storage manager grants the key access to a logical bucket.
   */
  scopes?: Array<'admin' | 'apps:read' | 'deploy:write' | 'secrets:read' | 'secrets:write' | 'usage:read' | 'env:read' | 'env:write' | 'registry_credentials:read' | 'registry_credentials:write' | 'upstreams:write' | 'storage:manage' | 'storage:read' | 'storage:write'>;
};

