/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteBudgetRequirement } from './RouteBudgetRequirement.js';
import type { RouteThrottleRequirement } from './RouteThrottleRequirement.js';
/**
 * At least one policy requirement; application authorization remains outside configuration verification.
 */
export type RouteChecks = {
  authentication?: 'consumer' | 'jwt' | 'application';
  throttle?: RouteThrottleRequirement;
  budget?: RouteBudgetRequirement;
};

