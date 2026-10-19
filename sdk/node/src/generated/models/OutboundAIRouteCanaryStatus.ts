/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIRouteCanary } from './OutboundAIRouteCanary.js';
import type { OutboundAIRouteTargetStats } from './OutboundAIRouteTargetStats.js';
/**
 * A canary with the route's first target's and its own counts since window_start.
 */
export type OutboundAIRouteCanaryStatus = {
  canary: OutboundAIRouteCanary;
  window_start: string;
  baseline: OutboundAIRouteTargetStats;
  canary_stats: OutboundAIRouteTargetStats;
};

