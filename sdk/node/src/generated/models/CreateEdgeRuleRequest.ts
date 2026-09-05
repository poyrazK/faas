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
 * Body shape for POST /v1/apps/{slug}/edge-rules.
 */
export type CreateEdgeRuleRequest = {
  match_host: string;
  match_path?: string;
  match_methods?: Array<string>;
  priority?: number;
  enabled?: boolean;
  kind: 'route' | 'rewrite' | 'redirect' | 'headers' | 'cors' | 'jwt' | 'ip' | 'validate' | 'limit' | 'maintenance' | 'geo' | 'throttle' | 'budget' | 'cache';
  /**
   * Top-level source of truth for kind=validate (ADR-128).
   * Omitted == 'block' (the SQL-side default).
   *
   */
  validate_mode?: 'block' | 'observe' | 'warn';
  /**
   * Kind-tagged action body — shape depends on `kind`.
   */
  action: (EdgeRuleRouteAction | EdgeRuleRewriteAction | EdgeRuleRedirectAction | EdgeRuleHeadersAction | EdgeRuleCORSAction | EdgeRuleJWTAction | EdgeRuleIPAction | EdgeRuleValidateAction | EdgeRuleLimitAction | EdgeRuleMaintenanceAction | EdgeRuleGeoAction | EdgeRuleThrottleAction | EdgeRuleBudgetAction | EdgeRuleCacheAction);
};

