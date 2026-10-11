/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessDecision } from './OperationBusinessDecision.js';
import type { OperationWorkflowConditionComparison } from './OperationWorkflowConditionComparison.js';
/**
 * Earliest retained report classifying this contributor for the selected decision outcome group. The optional business decision snapshot references its fact within operation_id. A path without a decision is evidenced by the report itself. These are application-reported facts and do not establish causation.
 */
export type OperationWorkflowDecisionOutcomeEvidence = {
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

