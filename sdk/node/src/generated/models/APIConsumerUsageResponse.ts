/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerMeterUsage } from './APIConsumerMeterUsage.js';
import type { APIConsumerRouteUsage } from './APIConsumerRouteUsage.js';
import type { APIConsumerUsageBucketResponse } from './APIConsumerUsageBucketResponse.js';
/**
 * Durable minute usage for one stable API consumer.
 */
export type APIConsumerUsageResponse = {
  route_breakdown?: Array<APIConsumerRouteUsage>;
  meter_breakdown?: Array<APIConsumerMeterUsage>;
  consumer_id: string;
  period_start: string;
  period_end: string;
  request_count: number;
  error_count: number;
  billable_units: number;
  buckets: Array<APIConsumerUsageBucketResponse>;
  as_of: string;
};

