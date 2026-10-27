/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The edge-rule change applying an active insight would make now, planned from its evidence and the app's current rules. Absent when the platform has no fix; never in webhook payloads.
 */
export type RouteInsightFix = {
  /**
   * Covers every value the fix would write; send it back to apply exactly this fix.
   */
  id: string;
  action: 'create_cache_rule' | 'set_cache_max_age' | 'add_cache_override' | 'disable_cache_rule';
  summary?: string;
  max_age_seconds?: number;
  /**
   * The cache rule the fix changes or overrides.
   */
  rule_id?: string;
  /**
   * The fix also purges the route's cached responses.
   */
  purge_cache?: boolean;
};

