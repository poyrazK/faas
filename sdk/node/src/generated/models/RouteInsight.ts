/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteInsightFix } from './RouteInsightFix.js';
/**
 * One finding for a route; also the payload of the routes.insight.detected and routes.insight.resolved webhooks.
 */
export type RouteInsight = {
  id: string;
  kind: 'kept_awake' | 'cacheable' | 'cache_stale' | 'cache_too_short';
  method: string;
  /**
   * Route template, for example /catalog.
   */
  route: string;
  state: 'active' | 'resolved';
  /**
   * What was observed and what to change.
   */
  message: string;
  /**
   * Measured figures behind the insight: awake share, interval and savings for kept_awake; safe max-age and repeat counts for cacheable; the rule's max-age, refills, stale refills and measured safe max-age for cache_stale and cache_too_short.
   */
  evidence: Record<string, any>;
  first_detected_at: string;
  last_detected_at: string;
  resolved_at?: string;
  /**
   * When an applied fix resolved the insight.
   */
  fixed_at?: string;
  /**
   * The fix that resolved the insight.
   */
  fix_action?: 'create_cache_rule' | 'set_cache_max_age' | 'add_cache_override' | 'disable_cache_rule';
  fix?: RouteInsightFix;
};

