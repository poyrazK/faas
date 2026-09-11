/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleResponse } from './EdgeRuleResponse.js';
import type { EdgeRuleSuggestion } from './EdgeRuleSuggestion.js';
/**
 * Deterministic OpenAPI policy plan or the result of an explicit apply.
 * Planned=true means no mutation occurred. AppliedCount is zero for an
 * idempotent no-op.
 *
 */
export type AppOpenAPIPolicyApplyResponse = {
  app_id: string;
  match_host: string;
  preview_sha256: string;
  suggestions: Array<EdgeRuleSuggestion>;
  planned: boolean;
  applied: Array<EdgeRuleResponse>;
  applied_count: number;
};

