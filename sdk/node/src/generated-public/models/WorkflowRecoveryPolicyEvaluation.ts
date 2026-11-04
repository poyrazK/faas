/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryFailureCoverage } from './WorkflowRecoveryFailureCoverage.js';
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
import type { WorkflowRecoveryPolicyEvaluationCandidate } from './WorkflowRecoveryPolicyEvaluationCandidate.js';
import type { WorkflowRecoveryPolicyEvaluationTotals } from './WorkflowRecoveryPolicyEvaluationTotals.js';
import type { WorkflowRecoveryPolicyResponse } from './WorkflowRecoveryPolicyResponse.js';
/**
 * Historical comparison under stated replay assumptions, with retained-evidence gaps and every bounded sample truncation disclosed.
 */
export type WorkflowRecoveryPolicyEvaluation = {
  failure_coverage: WorkflowRecoveryFailureCoverage;
  current: WorkflowRecoveryPolicyResponse;
  proposed: WorkflowRecoveryPolicy;
  observed_at: string;
  created_after: string;
  created_before: string;
  inspected_reviews: number;
  reviews_truncated: boolean;
  candidates_truncated: boolean;
  executions_truncated: boolean;
  observed_executions: number;
  current_totals: WorkflowRecoveryPolicyEvaluationTotals;
  proposed_totals: WorkflowRecoveryPolicyEvaluationTotals;
  candidates: Array<WorkflowRecoveryPolicyEvaluationCandidate>;
  coverage_notes: Array<string>;
};

