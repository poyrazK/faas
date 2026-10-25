/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRuleVersionOverlap } from './OperationWorkflowRuleVersionOverlap.js';
import type { OperationWorkflowRuleVersionStatistics } from './OperationWorkflowRuleVersionStatistics.js';
/**
 * One pinned contract, decision code and rule ID with side-by-side version columns. Compared status means at least two returned versions have eligible observations; it is not a significance test or a claim of improvement. At most 16 canonical identities and 16 lexical versions per identity are retained.
 */
export type OperationWorkflowRuleComparison = {
  /**
   * At least two returned versions need eligible single-version workflows to compare this subset.
   */
  single_version_status: 'compared' | 'insufficient_data';
  /**
   * Distinct sampled workflows reporting multiple versions for this comparison identity, including incomplete histories and versions omitted by limits.
   */
  multi_version_workflow_count: number;
  eligible_multi_version_workflow_count: number;
  examples_truncated: boolean;
  multi_version_examples: Array<OperationWorkflowRuleVersionOverlap>;
  contract_version: number;
  decision_code: string;
  rule_id: string;
  status: 'compared' | 'insufficient_data';
  versions_truncated: boolean;
  versions: Array<OperationWorkflowRuleVersionStatistics>;
};

