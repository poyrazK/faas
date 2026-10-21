/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleResponse } from './EdgeRuleResponse.js';
import type { RouteInsight } from './RouteInsight.js';
import type { RouteInsightFix } from './RouteInsightFix.js';
/**
 * An applied fix: the insight after it, the fix, the edge rule created or changed, the cache purge outcome, and how many of the route's insights it resolved.
 */
export type RouteInsightFixResponse = {
  insight: RouteInsight;
  fix: RouteInsightFix;
  rule: EdgeRuleResponse;
  cache_purge?: 'requested' | 'failed';
  resolved: number;
};

