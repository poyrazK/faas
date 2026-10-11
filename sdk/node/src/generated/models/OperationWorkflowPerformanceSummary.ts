/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowActiveConditionSummary } from './OperationWorkflowActiveConditionSummary.js';
import type { OperationWorkflowConditionCoverage } from './OperationWorkflowConditionCoverage.js';
import type { OperationWorkflowConditionOutcomeSummary } from './OperationWorkflowConditionOutcomeSummary.js';
import type { OperationWorkflowConditionRefreshProgress } from './OperationWorkflowConditionRefreshProgress.js';
import type { OperationWorkflowDecisionOutcomeSummary } from './OperationWorkflowDecisionOutcomeSummary.js';
import type { OperationWorkflowMissingConditionProgress } from './OperationWorkflowMissingConditionProgress.js';
import type { OperationWorkflowNewMissingConditionProgress } from './OperationWorkflowNewMissingConditionProgress.js';
import type { OperationWorkflowPerformanceCohort } from './OperationWorkflowPerformanceCohort.js';
/**
 * Completed and ongoing workflow cohorts with bounded sampling, history coverage and duration distributions.
 */
export type OperationWorkflowPerformanceSummary = {
  new_missing_progress?: OperationWorkflowNewMissingConditionProgress;
  missing_progress?: OperationWorkflowMissingConditionProgress;
  condition_coverage?: OperationWorkflowConditionCoverage;
  refresh_progress?: OperationWorkflowConditionRefreshProgress;
  active_conditions?: OperationWorkflowActiveConditionSummary;
  condition_outcomes?: OperationWorkflowConditionOutcomeSummary;
  decision_outcomes?: OperationWorkflowDecisionOutcomeSummary;
  /**
   * Opaque selector-bound token for opening consistent contributor views.
   */
  cohort_token: string;
  evaluated_at: string;
  workflow: string;
  cohort_limit: number;
  completed: OperationWorkflowPerformanceCohort;
  ongoing: OperationWorkflowPerformanceCohort;
};

