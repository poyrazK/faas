/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantOffboardingPlanActions } from './PlatformTenantOffboardingPlanActions.js';
/**
 * Confirmed offboarding actions and durable receipt created in the same transaction.
 */
export type PlatformTenantOffboardingApplyResponse = {
  tenant_id: string;
  receipt_id: string;
  plan_hash: string;
  applied_at: string;
  applied: boolean;
  actions: PlatformTenantOffboardingPlanActions;
};

