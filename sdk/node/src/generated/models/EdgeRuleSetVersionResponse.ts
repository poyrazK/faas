/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleResponse } from './EdgeRuleResponse.js';
/**
 * One recorded state of an app's whole edge-rule set (ADR-831).
 */
export type EdgeRuleSetVersionResponse = {
  version: number;
  rule_count: number;
  /**
   * Digest of the recorded rule set.
   */
  rules_sha256: string;
  created_at: string;
  /**
   * True for the app's latest version.
   */
  current: boolean;
  /**
   * The recorded rules; present only when a single version is fetched.
   */
  rules?: Array<EdgeRuleResponse>;
};

