/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDecisionOutcomeEvidence } from './OperationWorkflowDecisionOutcomeEvidence.js';
/**
 * Before and optional after reports for one baseline overdue condition. Results may change while matching exact contract and decision/rule/version and condition ID/description. Newer report is independent of current freshness. Removed identities and budgets are not evidence of refreshed verification. Contract changes have no comparable after fact.
 */
export type OperationWorkflowConditionRefreshProgressChange = {
  status: 'refreshed' | 'still_overdue' | 'unreported' | 'contract_changed' | 'budget_removed';
  newer_report: boolean;
  before: OperationWorkflowDecisionOutcomeEvidence;
  after?: OperationWorkflowDecisionOutcomeEvidence;
  explanation: string;
};

