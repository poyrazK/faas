/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessConditionResult } from './OperationBusinessConditionResult.js';
import type { OperationBusinessDecision } from './OperationBusinessDecision.js';
import type { OperationWorkflowConditionComparison } from './OperationWorkflowConditionComparison.js';
import type { OperationWorkflowConditionFreshness } from './OperationWorkflowConditionFreshness.js';
/**
 * Retained report classifying this contributor. Completed outcome groups use the earliest qualifying fact; active condition groups use the latest matching fact among current decision contexts. The optional business decision snapshot references its fact within operation_id. A path without a decision is evidenced by the report itself. These are application-reported facts and do not establish causation.
 */
export type OperationWorkflowDecisionOutcomeEvidence = {
  condition_freshness?: OperationWorkflowConditionFreshness;
  /**
   * Age of the selected active condition fact at evaluated_at; not a condition expiry or assertion of current truth.
   */
  evidence_age_seconds?: number;
  condition?: OperationBusinessConditionResult;
  /**
   * All results reported before or at the first terminal report for the selected exact contract and decision and rule and condition ID/description identity. Present on condition contributors; multiple results explain overlapping group membership.
   */
  observed_condition_results?: Array<'pass' | 'fail' | 'unknown'>;
  condition_comparison?: OperationWorkflowConditionComparison;
  report_id: string;
  operation_id: string;
  revision: number;
  from_state?: string;
  state: string;
  occurred_at: string;
  milestone_id?: string;
  decision?: OperationBusinessDecision;
};

