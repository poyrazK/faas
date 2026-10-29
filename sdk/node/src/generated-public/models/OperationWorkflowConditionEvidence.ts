/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact retained report and decision fact reference with reported rule identity. Previous evidence is absent when the baseline did not report a decision in this context. Links refer to the current retained Operation view.
 */
export type OperationWorkflowConditionEvidence = {
  report_id: string;
  operation_id: string;
  revision: number;
  milestone_id: string;
  decision_code: string;
  rule_id: string;
  rule_version: string;
  /**
   * Selected path when the decision was reported for a path group.
   */
  path_id?: string;
};

