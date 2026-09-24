/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantUsageBucketResponse } from './PlatformTenantUsageBucketResponse.js';
/**
 * Cross-app raw usage totals with app and consumer attribution.
 */
export type PlatformTenantUsageResponse = {
  tenant_id: string;
  period_start: string;
  period_end: string;
  request_count: number;
  error_count: number;
  billable_units: number;
  buckets: Array<PlatformTenantUsageBucketResponse>;
  as_of: string;
};

