/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleCORSAction } from './EdgeRuleCORSAction.js';
import type { EdgeRuleHeadersAction } from './EdgeRuleHeadersAction.js';
/**
 * Named environment-scoped headers or CORS rule.
 */
export type EnvironmentPolicy = {
  name: string;
  kind: 'headers' | 'cors';
  match_path: string;
  match_methods?: Array<string>;
  match_headers?: Record<string, string>;
  priority: number;
  enabled?: boolean;
  action: (EdgeRuleHeadersAction | EdgeRuleCORSAction);
};

