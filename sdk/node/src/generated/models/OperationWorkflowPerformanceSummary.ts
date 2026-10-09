/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPerformanceCohort } from './OperationWorkflowPerformanceCohort.js';
export type OperationWorkflowPerformanceSummary = {
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

