/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One API key in export form: id, prefix (first 8 chars), label, scopes, created/last-used timestamps, and request count. Scopes is the permission set attached to the key at the moment of export (audit trail; IAM-1, ADR-034 rev2).
 */
export type APIKeyExportResponse = {
  id: string;
  prefix: string;
  label?: string | null;
  /**
   * Permission set attached to the exported key. Object-storage data scopes additionally require an explicit per-bucket grant; admin remains full access.
   */
  scopes: Array<'admin' | 'apps:read' | 'deploy:write' | 'secrets:read' | 'secrets:write' | 'usage:read' | 'env:read' | 'env:write' | 'registry_credentials:read' | 'registry_credentials:write' | 'upstreams:write' | 'storage:manage' | 'storage:read' | 'storage:write' | 'postgres:manage' | 'postgres:read'>;
  created_at: string;
  last_used_at?: string | null;
};

