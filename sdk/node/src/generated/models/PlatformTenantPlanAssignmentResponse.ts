/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One plan assignment and the tenant rate card it created.
 */
export type PlatformTenantPlanAssignmentResponse = {
  id: string;
  tenant_id: string;
  plan_id: string;
  plan_name?: string;
  rate_card_id: string;
  effective_from: string;
  created_at: string;
};

