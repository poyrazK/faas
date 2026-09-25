/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One UTC day of app-consumer usage from the durable raw usage ledger.
 */
export type PlatformTenantUsageBucketResponse = {
  app_id: string;
  consumer_id?: string;
  surface_id?: string;
  window_start: string;
  request_count: number;
  error_count: number;
  billable_units: number;
};

