/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantReconciliationPlanChange } from './PlatformTenantReconciliationPlanChange.js';
/**
 * A deterministic, read-only plan. Applying or approving a plan is a separate future operation.
 */
export type PlatformTenantReconciliationPlanResponse = {
  tenant_id: string;
  changes: Array<PlatformTenantReconciliationPlanChange>;
};

