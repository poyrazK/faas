/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selection of workflow runs for a preview or unstarted cancellation.
 */
export type WorkflowQueuedRunCancelRequest = {
  run_ids: Array<string>;
  /**
   * Optional exact workflow-name guard for the selection.
   */
  workflow_name?: string;
};

