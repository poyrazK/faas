/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GlobalRuntimeQuotaPolicy } from './GlobalRuntimeQuotaPolicy.js';
import type { GlobalRuntimeQuotaTargetResponse } from './GlobalRuntimeQuotaTargetResponse.js';
/**
 * Directory capacity accounting. Reservation and issuance timestamps do not imply running instances, readiness or ingress.
 */
export type GlobalRuntimeQuotaResponse = {
  global_app_id: string;
  allocation_id: string;
  generation: number;
  policy: GlobalRuntimeQuotaPolicy;
  status: 'reserved' | 'retiring' | 'retired';
  created_at: string;
  retirement_id: string | null;
  retired_at: string | null;
  targets: Array<GlobalRuntimeQuotaTargetResponse>;
};

