/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only threshold finding evaluated at the attention cursor time against the current retained report and its pinned definition. Owner is the recommended escalation recipient, not the blocker assignee.
 */
export type OperationWorkflowBlockerEscalation = {
  code: string;
  operation: string;
  owner: string;
  after_seconds: number;
  /**
   * Time when the reported blocker first reached its declared threshold.
   */
  escalated_at: string;
};

