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
   * Permission set attached to the key. Closed vocabulary (IAM-1, ADR-034 rev2): `admin` is the legacy full-access scope; `apps:read` covers GETs across the apps/deployments/audit/secrets-list surface; `deploy:write` covers POST/PUT/PATCH/DELETE on apps+queues; `secrets:write` covers PUT/DELETE on /apps/{slug}/secrets/{key}; `usage:read` covers GET /v1/usage*.
   */
  scopes: Array<'admin' | 'deploy:write' | 'secrets:read' | 'secrets:write' | 'usage:read' | 'apps:read'>;
  created_at: string;
  last_used_at?: string | null;
};

