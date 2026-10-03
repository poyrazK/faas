/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantReconciliationPlanChange } from './PlatformTenantReconciliationPlanChange.js';
/**
 * A deterministic, read-only plan and digest. The digest confirms this desired bundle and current ownership-aware state at apply time.
 */
export type PlatformTenantReconciliationPlanResponse = {
  tenant_id: string;
  /**
   * SHA-256 confirmation token for this desired bundle and current plan.
   */
  plan_hash: string;
  changes: Array<PlatformTenantReconciliationPlanChange>;
};

