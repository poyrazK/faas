/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessConditionDefinition } from './OperationBusinessConditionDefinition.js';
import type { OperationWorkflowDecisionOutcomeEvidence } from './OperationWorkflowDecisionOutcomeEvidence.js';
/**
 * One condition expected in the current state. Match the declaring workflow step and exact condition ID and description within the current contract and latest explicit contexts. Evidence keeps the source report pinned age budget and decision identity. Reported includes any application result and may have no freshness budget; overdue is evidence past its budget. Missing has no evidence. This grants no authorization or verification.
 */
export type OperationWorkflowConditionCoverageEntry = {
  step: string;
  milestone: string;
  definition: OperationBusinessConditionDefinition;
  status: 'reported' | 'missing' | 'overdue';
  evidence?: OperationWorkflowDecisionOutcomeEvidence;
};

