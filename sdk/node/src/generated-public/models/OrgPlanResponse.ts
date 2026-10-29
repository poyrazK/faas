/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provider-confirmed organization entitlements and any pending change.
 */
export type OrgPlanResponse = {
  org_id: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  plan_change_status?: string;
  requested_plan?: string;
  effective_at?: string;
};

