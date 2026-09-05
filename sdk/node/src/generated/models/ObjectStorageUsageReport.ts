/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Authoritative cumulative provider usage for one account, backend and UTC month. Missing data is not zero; costs are EUR millicents.
 */
export type ObjectStorageUsageReport = {
  account_id: string;
  backend_id: string;
  backend_fingerprint: string;
  source: string;
  period_start: string;
  observed_at: string;
  stored_byte_hours: number;
  request_count: number;
  egress_bytes: number;
  cost_millicents: number;
};

