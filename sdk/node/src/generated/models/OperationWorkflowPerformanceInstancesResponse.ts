/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
import type { OperationWorkflowPerformanceCoverageReason } from './OperationWorkflowPerformanceCoverageReason.js';
import type { OperationWorkflowPerformanceGroup } from './OperationWorkflowPerformanceGroup.js';
import type { OperationWorkflowPerformanceInstance } from './OperationWorkflowPerformanceInstance.js';
/**
 * Coverage counts cover the selected cohort before dimension selection. Duration statistics and ranked items include only complete-history instances contributing to the exact dimension. Overall dimensions include eligible zero values. The token binds both sampled cohorts and evidence at evaluated_at; it does not store a database snapshot.
 */
export type OperationWorkflowPerformanceInstancesResponse = {
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

