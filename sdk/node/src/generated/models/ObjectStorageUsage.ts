/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-wide usage and reserved capacity. In gateway_safety_v1 requests count durable provider attempts and egress counts full response lengths reserved before forwarding; disconnects retain reservations. Fields named in unavailable_meters are unknown, their numeric placeholders are not measured zeros and must not be billed. Legacy costs are EUR millicents, not a customer invoice.
 */
export type ObjectStorageUsage = {
  unavailable_meters?: Array<'stored_byte_hours' | 'cost_millicents'>;
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

