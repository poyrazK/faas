/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookDependencyState } from './OperationWorkflowPlaybookDependencyState.js';
/**
 * OperationWorkflowPlaybookDependencyRegression. Durable sampled dependency regression grouped by affected checklist owner. Existing queue assignments, reviews and clocks remain unchanged.
 */
export type OperationWorkflowPlaybookDependencyRegression = {
  /**
   * OperationWorkflowPlaybookDependencyRegression.checked_at. Successful case observation before revision-fenced publication.
   */
  checked_at: string;
  /**
   * OperationWorkflowPlaybookDependencyRegression.reopened_prerequisites. Relevant ancestors observed changing from completed to pending, including transitive causes.
   */
  reopened_prerequisites: Array<OperationWorkflowPlaybookDependencyState>;
  /**
   * OperationWorkflowPlaybookDependencyRegression.affected_steps. Downstream steps newly waiting, gaining blockers, or affected by a newly reopened ancestor; completed reviews retain a review suggestion.
   */
  affected_steps: Array<OperationWorkflowPlaybookDependencyState>;
};

