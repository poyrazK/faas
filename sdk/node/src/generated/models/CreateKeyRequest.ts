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
   * Requested permission set. Server validates each entry against the closed vocabulary and rejects unknown scopes at mint time. `admin` is the legacy full-access scope; the other five cover narrower surfaces (see APIKeyResponse.scopes). See IAM-1, ADR-034 rev2.
   */
  scopes?: Array<'admin' | 'deploy:write' | 'secrets:read' | 'secrets:write' | 'usage:read' | 'apps:read'>;
};

