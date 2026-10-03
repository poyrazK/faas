/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Verified regional app identity with current registry resolution status. This is not readiness or health.
 */
export type GlobalAppTargetResponse = {
  cell_id: string;
  region: string;
  cell_generation: number;
  regional_account_id: string;
  regional_app_id: string;
  regional_app_slug: string;
  verified_at: string;
  resolution_status: 'verified' | 'cell_disabled' | 'reverify_required';
};

