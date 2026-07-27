/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * API key metadata: id, prefix (first 8 chars), label, scopes, created/last-used timestamps, request count. **Plaintext is returned only on POST**.
 */
export type APIKeyResponse = {
  id: string;
  /**
   * First 16 chars of the key (e.g. `fp_live_abc12345…`).
   */
  prefix: string;
  label?: string | null;
  /**
   * Permission set attached to the key. Closed vocabulary (IAM-1, ADR-034 rev2): admin is the legacy full-access scope; apps:read covers GETs across the apps/deployments/audit/secrets-list surface; deploy:write covers POST/PUT/PATCH/DELETE on apps+queues; secrets:write covers PUT/DELETE on /apps/{slug}/secrets/{key}; usage:read covers GET /v1/usage*.
   */
  scopes: Array<'admin' | 'deploy:write' | 'secrets:read' | 'secrets:write' | 'usage:read' | 'apps:read'>;
  last_used_at?: string | null;
  created_at: string;
  /**
   * PRESENT ONLY on POST /v1/keys response. Never returned again.
   */
  plaintext?: string | null;
};

