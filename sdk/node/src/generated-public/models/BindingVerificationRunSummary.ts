/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Compact retained receipt identity and stored state, with expiry derived at the page observation time. Passed does not assert current evidence qualification. No resource output, plans, policy or manifest is included.
 */
export type BindingVerificationRunSummary = {
  id: string;
  app?: string;
  deployment_id?: string;
  scope?: string;
  graph_run_id?: string;
  project?: string;
  environment?: string;
  status: 'pending' | 'passed' | 'cancelled' | 'expired';
  created_at: string;
  expires_at: string;
};

