/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator safety limits. Empty accounting_mode requires qualified provider reports including cost. Explicit gateway_safety_v1 requires a coverage start, proxied transfers, billing off and no cost ceiling; all other budgets remain positive and finite.
 */
export type ObjectStoragePolicy = {
  accounting_mode?: 'gateway_safety_v1';
  gateway_metering_since?: string;
  max_account_bytes: number;
  max_bucket_bytes: number;
  max_account_keys: number;
  max_monthly_cost_millicents: number;
  max_monthly_requests: number;
  max_monthly_egress_bytes: number;
  max_monthly_authorizations: number;
  max_report_age_seconds: number;
};

