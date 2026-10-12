/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookStepAssignmentPolicy. Pinned team and completion window for one declared outcome follow-up step; coordination only.
 */
export type OperationWorkflowPlaybookStepAssignmentPolicy = {
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.label. Exact unique declared follow-up label.
   */
  label: string;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.owner. Required trimmed control-free UTF-8 team label.
   */
  owner: string;
  /**
   * OperationWorkflowPlaybookStepAssignmentPolicy.complete_after_seconds. Completion window from first list review; bounded to seven days.
   */
  complete_after_seconds: number;
};

