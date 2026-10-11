/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RoutePriorityRule } from './RoutePriorityRule.js';
/**
 * An app's effective route priorities and where they come from.
 */
export type RoutePrioritiesResponse = {
  slug: string;
  source: 'configured' | 'route_health' | 'none';
  routes: Array<RoutePriorityRule>;
  /**
   * When saved rules last changed; omitted for the route-health default.
   */
  updated_at?: string;
};

