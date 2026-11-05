/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRuleComparison } from './OperationWorkflowRuleComparison.js';
/**
 * Side-by-side rule version statistics from the same bounded completed workflow sample. Observed identities are scoped to contract, decision code and rule ID. Version names are opaque and sorted lexically, not chronologically. Workflows may report several versions and contribute to several columns; comparisons do not establish causation or statistical significance. Coverage of unobserved versions cannot be inferred from missing history.
 */
export type OperationWorkflowRuleComparisonSummary = {
  sampled_workflow_count: number;
  /**
   * Sampled workflows without an observed decision identity in the retained window. Includes missing history and paths without decisions; absence does not prove a rule was unused.
   */
  unattributed_workflow_count: number;
  comparisons_truncated: boolean;
  comparisons: Array<OperationWorkflowRuleComparison>;
};

