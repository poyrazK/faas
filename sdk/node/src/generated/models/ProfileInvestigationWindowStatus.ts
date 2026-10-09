/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Query eligibility under the current plan and installation. Retained does not guarantee that samples exist; expiry never implies zero CPU usage.
 */
export type ProfileInvestigationWindowStatus = {
  status: 'retained' | 'expired' | 'deployment_unavailable' | 'plan_unavailable' | 'backend_unavailable';
  detail: string;
};

