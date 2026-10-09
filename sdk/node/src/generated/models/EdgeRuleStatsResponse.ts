/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleHitStatsResponse } from './EdgeRuleHitStatsResponse.js';
/**
 * Per-rule match counts over a window (ADR-830).
 */
export type EdgeRuleStatsResponse = {
  window: '1h' | '24h' | '7d';
  since: string;
  rules: Array<EdgeRuleHitStatsResponse>;
};

