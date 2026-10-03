/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantOffboardingPlanActions } from './PlatformTenantOffboardingPlanActions.js';
/**
 * Read-only preview of the status, confirmation digest, and planned offboarding actions for a platform tenant.
 */
export type PlatformTenantOffboardingPlanResponse = {
  tenant_id: string;
  status: 'active' | 'suspended';
  plan_hash: string;
  actions: PlatformTenantOffboardingPlanActions;
};

