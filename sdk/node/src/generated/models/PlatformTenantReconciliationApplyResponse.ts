/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantReconciliationPlanChange } from './PlatformTenantReconciliationPlanChange.js';
/**
 * The confirmed changes applied in one transaction. Consumers and surfaces are detached, not deleted.
 */
export type PlatformTenantReconciliationApplyResponse = {
  tenant_id: string;
  plan_hash: string;
  /**
   * True when the confirmed plan completed
   */
  applied: boolean;
  changes: Array<PlatformTenantReconciliationPlanChange>;
};

