/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/orgs/{slug}/keys body. Mirrors `CreateKeyRequest` (PR 6 keeps them in lockstep) — label + optional scopes. Empty `scopes` defaults to `["admin"]` so existing callers preserve the legacy full-access behavior. See IAM-1, ADR-034 rev2.
 */
export type CreateOrgAPIKeyRequest = {
  label?: string;
  /**
   * Requested permission set for the org-scoped key. Unknown scopes are rejected; object-storage data scopes also require an explicit logical-bucket grant. The legacy and org-scoped key vocabularies remain identical.
   */
  scopes?: Array<'admin' | 'apps:read' | 'deploy:write' | 'secrets:read' | 'secrets:write' | 'usage:read' | 'env:read' | 'env:write' | 'registry_credentials:read' | 'registry_credentials:write' | 'upstreams:write' | 'storage:manage' | 'storage:read' | 'storage:write'>;
};

