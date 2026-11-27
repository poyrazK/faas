/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerPlanRouteLimit } from './APIConsumerPlanRouteLimit.js';
/**
 * Named consumer plan. Zero limits are unlimited.
 */
export type CreateAPIConsumerPlanRequest = {
  name: string;
  /**
   * Admitted requests per consumer per minute; over the limit the gateway returns 429.
   */
  max_requests_per_minute?: number;
  /**
   * Weighted units per consumer per UTC month; over the cap the gateway returns 429 until the month ends.
   */
  max_units_per_month?: number;
  /**
   * Percentages of max_units_per_month at which a consumer.usage_threshold webhook fires, once per consumer per UTC month. Requires max_units_per_month.
   */
  alert_thresholds_percent?: Array<number>;
  /**
   * Per-consumer caps on single billing routes, keyed by route label: an HTTP method and path template, or "MCP /tools/<name>" for one MCP tool. At most 50.
   */
  route_limits?: Record<string, APIConsumerPlanRouteLimit>;
};

