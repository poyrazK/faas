/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata for a per-app deploy token. The fp_deploy_ plaintext is returned only on create and rotate responses.
 */
export type DeployTokenResponse = {
  id: string;
  app_id: string;
  prefix: string;
  label?: string;
  scopes: Array<'deploy:write'>;
  status: 'active' | 'grace' | 'revoked';
  created_at: string;
  expires_at: string;
  last_used_at?: string;
  revoked_at?: string;
  rotated_from_id?: string;
  /**
   * Present only on create/rotate. Store it in CI immediately; it cannot be retrieved later.
   */
  plaintext?: string;
};

