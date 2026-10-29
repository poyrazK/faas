/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowActiveConditionSummary } from './OperationWorkflowActiveConditionSummary.js';
import type { OperationWorkflowConditionCoverage } from './OperationWorkflowConditionCoverage.js';
import type { OperationWorkflowConditionOutcomeSummary } from './OperationWorkflowConditionOutcomeSummary.js';
import type { OperationWorkflowDecisionOutcomeSummary } from './OperationWorkflowDecisionOutcomeSummary.js';
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
import type { OperationWorkflowNewMissingConditionProgress } from './OperationWorkflowNewMissingConditionProgress.js';
import type { OperationWorkflowPerformanceCoverageReason } from './OperationWorkflowPerformanceCoverageReason.js';
import type { OperationWorkflowPerformanceGroup } from './OperationWorkflowPerformanceGroup.js';
import type { OperationWorkflowPerformanceInstance } from './OperationWorkflowPerformanceInstance.js';
/**
 * Coverage counts cover the selected cohort before dimension selection. Duration statistics and ranked items include only complete-history instances contributing to the exact dimension. Overall dimensions include eligible zero values. The token binds both sampled cohorts and evidence at evaluated_at; it does not store a database snapshot.
 */
export type OperationWorkflowPerformanceInstancesResponse = {
  new_missing_progress?: OperationWorkflowNewMissingConditionProgress;
  condition_coverage?: OperationWorkflowConditionCoverage;
  active_conditions?: OperationWorkflowActiveConditionSummary;
  condition_outcomes?: OperationWorkflowConditionOutcomeSummary;
  decision_outcomes?: OperationWorkflowDecisionOutcomeSummary;
  evaluated_at: string;
  cohort_token: string;
  workflow: string;
  cohort: 'completed' | 'ongoing';
  group: OperationWorkflowPerformanceGroup;
  matching_workflow_count: number;
  sampled_workflow_count: number;
  complete_history_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  cohort_truncated: boolean;
  exclusions: Array<OperationWorkflowPerformanceCoverageReason>;
  duration: OperationWorkflowDurationDistribution;
  items: Array<OperationWorkflowPerformanceInstance>;
};

