/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateEdgeRuleRequest } from './CreateEdgeRuleRequest.js';
import type { RouteAdviceEvidence } from './RouteAdviceEvidence.js';
import type { RouteAdviceImpact } from './RouteAdviceImpact.js';
import type { RouteAdviceSetting } from './RouteAdviceSetting.js';
/**
 * One proposed edge rule for one observed route, with evidence, a what-if estimate and disabled rule bodies (one per hostname). Kinds wake_ahead and priority are app-level settings (ADR-1152): method and route are empty, rules is empty and setting carries the proposal.
 */
export type RouteAdviceSuggestion = {
  /**
   * Stable for the same kind, method and route.
   */
  id: string;
  kind: 'cache' | 'async' | 'throttle' | 'wake_ahead' | 'priority';
  method: string;
  /**
   * Observed route template, for example /users/{id}.
   */
  route: string;
  title: string;
  rationale: string;
  evidence: RouteAdviceEvidence;
  impact: RouteAdviceImpact;
  cautions?: Array<string>;
  rules: Array<CreateEdgeRuleRequest>;
  setting?: RouteAdviceSetting;
};

