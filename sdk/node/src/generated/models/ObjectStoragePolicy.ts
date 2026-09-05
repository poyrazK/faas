/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator safety limits; zero values mean unconfigured and block signing.
 */
export type ObjectStoragePolicy = {
  max_account_bytes: number;
  max_bucket_bytes: number;
  max_account_keys: number;
  max_monthly_cost_millicents: number;
  max_monthly_requests: number;
  max_monthly_egress_bytes: number;
  max_monthly_authorizations: number;
  max_report_age_seconds: number;
};

