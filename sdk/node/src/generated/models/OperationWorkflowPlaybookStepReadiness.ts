/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookStepReadiness. Human review dependency status, not business state or execution authority.
 */
export type OperationWorkflowPlaybookStepReadiness = {
  /**
   * OperationWorkflowPlaybookStepReadiness.status. Whether all declared prerequisite reviews are complete and their dependency chains satisfied.
   */
  status: 'ready' | 'waiting';
  /**
   * OperationWorkflowPlaybookStepReadiness.waiting_for. Direct prerequisites currently missing complete and ready reviews; empty for ready steps.
   */
  waiting_for: Array<string>;
  /**
   * OperationWorkflowPlaybookStepReadiness.review_suggested. A previously completed dependent step now has unsatisfied prerequisites; original review is preserved.
   */
  review_suggested: boolean;
};

