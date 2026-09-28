/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantReconciliationPlanChange } from './PlatformTenantReconciliationPlanChange.js';
/**
 * Immutable, secret-free result of one successfully applied reconciliation plan.
 */
export type PlatformTenantReconciliationReceiptResponse = {
  tenant_id: string;
  receipt_id: string;
  plan_hash: string;
  applied_at: string;
  changes: Array<PlatformTenantReconciliationPlanChange>;
};

