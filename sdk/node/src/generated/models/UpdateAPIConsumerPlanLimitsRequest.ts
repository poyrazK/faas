/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIConsumerPlanRouteLimit } from './APIConsumerPlanRouteLimit.js';
/**
 * Replacement limits for a consumer plan. Omitting alert_thresholds_percent keeps the plan's thresholds; an empty list clears them.
 */
export type UpdateAPIConsumerPlanLimitsRequest = {
  max_requests_per_minute: number;
  max_units_per_month: number;
  /**
   * Replacement alert thresholds, as percentages of max_units_per_month (at most 5). Omit to keep the current thresholds; send an empty list to remove them.
   */
  alert_thresholds_percent?: Array<number>;
  /**
   * Replacement route limits. Omit to keep the current ones; send an empty object to remove them.
   */
  route_limits?: Record<string, APIConsumerPlanRouteLimit>;
};

