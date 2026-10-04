/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable delegation identity and current status; contains no secret or hash.
 */
export type RegionalReleaseGrantResponse = {
  grant_id: string;
  cell_id: string;
  regional_app_id: string;
  global_app_id: string;
  expires_at: string;
  status: 'active' | 'revoked' | 'expired';
};

