/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleBudgetAction } from './EdgeRuleBudgetAction.js';
import type { EdgeRuleCacheAction } from './EdgeRuleCacheAction.js';
import type { EdgeRuleCORSAction } from './EdgeRuleCORSAction.js';
import type { EdgeRuleGeoAction } from './EdgeRuleGeoAction.js';
import type { EdgeRuleHeadersAction } from './EdgeRuleHeadersAction.js';
import type { EdgeRuleIPAction } from './EdgeRuleIPAction.js';
import type { EdgeRuleJWTAction } from './EdgeRuleJWTAction.js';
import type { EdgeRuleLimitAction } from './EdgeRuleLimitAction.js';
import type { EdgeRuleMaintenanceAction } from './EdgeRuleMaintenanceAction.js';
import type { EdgeRuleRedirectAction } from './EdgeRuleRedirectAction.js';
import type { EdgeRuleRewriteAction } from './EdgeRuleRewriteAction.js';
import type { EdgeRuleRouteAction } from './EdgeRuleRouteAction.js';
import type { EdgeRuleThrottleAction } from './EdgeRuleThrottleAction.js';
import type { EdgeRuleValidateAction } from './EdgeRuleValidateAction.js';
/**
 * Partial update — every field optional. Kind is not patchable.
 */
export type UpdateEdgeRuleRequest = {
  match_host?: string;
  match_path?: string;
  match_methods?: Array<string>;
  priority?: number;
  enabled?: boolean;
  /**
   * Top-level source of truth for kind=validate (ADR-128).
   * Omit (do not send) to leave the column untouched.
   *
   */
  validate_mode?: 'block' | 'observe' | 'warn';
  /**
   * Replaces the jsonb column whole.
   */
  action?: (EdgeRuleRouteAction | EdgeRuleRewriteAction | EdgeRuleRedirectAction | EdgeRuleHeadersAction | EdgeRuleCORSAction | EdgeRuleJWTAction | EdgeRuleIPAction | EdgeRuleValidateAction | EdgeRuleLimitAction | EdgeRuleMaintenanceAction | EdgeRuleGeoAction | EdgeRuleThrottleAction | EdgeRuleBudgetAction | EdgeRuleCacheAction);
};

