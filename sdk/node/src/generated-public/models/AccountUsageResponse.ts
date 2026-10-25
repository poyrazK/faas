/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresUsageResponse } from './ManagedPostgresUsageResponse.js';
import type { ObjectStorageUsageResponse } from './ObjectStorageUsageResponse.js';
import type { UsageSummaryResponse } from './UsageSummaryResponse.js';
/**
 * Account-level usage projection across compute and the optional object-storage and managed-PostgreSQL services.
 */
export type AccountUsageResponse = {
  month: string;
  compute: UsageSummaryResponse;
  /**
   * Object-storage usage and safety policy when object storage is configured; omitted otherwise.
   */
  object_storage?: ObjectStorageUsageResponse;
  /**
   * Managed-PostgreSQL usage and guardrail state when the account plan includes it; omitted otherwise.
   */
  managed_postgres?: ManagedPostgresUsageResponse;
};

