/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantOffboardingPlanActions } from './PlatformTenantOffboardingPlanActions.js';
/**
 * Immutable secret-free result of one successful confirmed offboarding.
 */
export type PlatformTenantOffboardingReceiptResponse = {
  tenant_id: string;
  receipt_id: string;
  plan_hash: string;
  applied_at: string;
  actions: PlatformTenantOffboardingPlanActions;
};

