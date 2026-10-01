/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FailureRule } from './FailureRule.js';
/**
 * Versioned explicit classification policy for failed partition attempts.
 */
export type FailureRules = {
  version: 1;
  rules: Array<FailureRule>;
  unmatched_failure: 'retry' | 'fail_partition';
  /**
   * Choose whether a missing completion receipt stays held for reconciliation or is retried with possible duplicate execution.
   */
  uncertain_outcome: 'hold' | 'retry';
};

