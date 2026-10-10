/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantReconciliationPlanChange } from './PlatformTenantReconciliationPlanChange.js';
/**
 * The confirmed changes applied in one transaction. Consumers and surfaces are detached, not deleted. receipt_id can be used to recover the result later.
 */
export type PlatformTenantReconciliationApplyResponse = {
  tenant_id: string;
  /**
   * Durable identifier for this successful apply.
   */
  receipt_id: string;
  plan_hash: string;
  applied_at: string;
  /**
   * True when the confirmed plan completed, including an already-converged no-op.
   */
  applied: boolean;
  changes: Array<PlatformTenantReconciliationPlanChange>;
};

