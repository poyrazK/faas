/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookDependencyState } from './OperationWorkflowPlaybookDependencyState.js';
import type { OperationWorkflowReadinessOutcomeEscalationReference } from './OperationWorkflowReadinessOutcomeEscalationReference.js';
/**
 * OperationWorkflowPlaybookDependencyRecovery. Durable sampled waiting-to-ready observation for previously regressed steps, linked to the latest regression notice. Original reviews, queue assignments and clocks remain unchanged.
 */
export type OperationWorkflowPlaybookDependencyRecovery = {
  /**
   * OperationWorkflowPlaybookDependencyRecovery.checked_at. Successful case observation before revision-fenced publication.
   */
  checked_at: string;
  /**
   * OperationWorkflowPlaybookDependencyRecovery.regression. Original notice identity within the same inbox; the referenced row may have expired.
   */
  regression: OperationWorkflowReadinessOutcomeEscalationReference;
  /**
   * OperationWorkflowPlaybookDependencyRecovery.ready_steps. Recovered subset grouped by current checklist owner and regression notice; completed reviews remain complete.
   */
  ready_steps: Array<OperationWorkflowPlaybookDependencyState>;
};

