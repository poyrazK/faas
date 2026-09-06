/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-wide usage and reserved capacity. Costs are EUR millicents, not a customer invoice.
 */
export type ObjectStorageUsage = {
  observed_bytes: number;
  capacity_bytes: number;
  capacity_keys: number;
  stored_byte_hours: number;
  request_count: number;
  egress_bytes: number;
  cost_millicents: number;
  authorizations: number;
  fresh: boolean;
  period_start: string;
};

