/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One customer's authoritative cross-app admitted-request budget and current UTC counters. Absent policy has configured=false and zero ceilings.
 */
export type PlatformTenantRequestBudgetResponse = {
  tenant_id: string;
  configured: boolean;
  max_requests_per_minute: number;
  max_requests_per_day: number;
  minute_used: number;
  day_used: number;
  minute_resets_at: string;
  day_resets_at: string;
  updated_at?: string;
};

