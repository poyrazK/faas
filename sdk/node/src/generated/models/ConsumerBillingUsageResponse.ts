/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerMeterUsage } from './APIConsumerMeterUsage.js';
import type { APIConsumerRouteUsage } from './APIConsumerRouteUsage.js';
import type { ConsumerBillingDay } from './ConsumerBillingDay.js';
/**
 * A consumer's daily usage over at most 31 days.
 */
export type ConsumerBillingUsageResponse = {
  route_breakdown?: Array<APIConsumerRouteUsage>;
  meter_breakdown?: Array<APIConsumerMeterUsage>;
  since: string;
  until: string;
  days: Array<ConsumerBillingDay>;
};

