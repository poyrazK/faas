/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable regional release target and durable sanitized outcome.
 */
export type GlobalImageReleaseTargetResponse = {
  cell_id: string;
  region: string;
  cell_generation: number;
  regional_account_id: string;
  /**
   * Regional organization retained by this release snapshot; omitted for personal apps.
   */
  regional_org_id?: string;
  regional_app_id: string;
  regional_app_slug: string;
  outcome: 'pending' | 'accepted' | 'expired' | 'blocked';
  diagnostic: '' | 'regional_unavailable' | 'regional_rejected' | 'identity_changed' | 'receipt_invalid';
  deployment_id: string | null;
  updated_at: string;
};

