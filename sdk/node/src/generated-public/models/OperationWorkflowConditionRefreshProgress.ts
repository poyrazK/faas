/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowConditionRefreshProgressCount } from './OperationWorkflowConditionRefreshProgressCount.js';
import type { OperationWorkflowConditionRefreshProgressInstance } from './OperationWorkflowConditionRefreshProgressInstance.js';
/**
 * Report freshness progress reconstructed at baseline_token evaluation time using publication visibility in current retained completed and ongoing cohorts. This is not a stored baseline cohort, a total historical backlog, or business verification. Only continuous retained report history qualifies; contract boundaries are permitted for explicit contract_changed classification without relaxing existing duration or active-summary eligibility. Unknown history and conflicting or over-limit evidence are excluded. Status totals include all baseline overdue identities before per-workflow preview limits. Completed workflows remain visible as terminal reports without implying cleared verification.
 */
export type OperationWorkflowConditionRefreshProgress = {
  baseline_at: string;
  evaluated_at: string;
  sampled_workflow_count: number;
  compared_workflow_count: number;
  no_baseline_workflow_count: number;
  no_overdue_baseline_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  excluded_ambiguous_workflow_count: number;
  excluded_condition_evidence_workflow_count: number;
  newer_report_condition_count: number;
  statuses: Array<OperationWorkflowConditionRefreshProgressCount>;
  /**
   * Compared workflows in stable workflow identity order from current samples of up to 100 completed and 100 ongoing runs. Missing original baseline members are not classified as cleared.
   */
  items: Array<OperationWorkflowConditionRefreshProgressInstance>;
};

