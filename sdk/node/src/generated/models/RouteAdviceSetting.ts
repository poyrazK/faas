/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RoutePriorityRule } from './RoutePriorityRule.js';
/**
 * The app setting a wake_ahead or priority suggestion proposes; apply it through PUT /v1/apps/{slug}/service-wake-ahead or PUT /v1/apps/{slug}/route-priorities.
 */
export type RouteAdviceSetting = {
  /**
   * Turn service wake-ahead on.
   */
  wake_ahead?: boolean;
  route_priorities?: Array<RoutePriorityRule>;
};

