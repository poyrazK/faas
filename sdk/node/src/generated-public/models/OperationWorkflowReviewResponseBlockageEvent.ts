/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sampled blocker changes and positively verified recoveries for a linked response plan. Recovery does not resolve the response or establish a business result.
 */
export type OperationWorkflowReviewResponseBlockageEvent = {
  /**
   * OperationWorkflowReviewResponseBlockageEvent.source_notification_id. Original response alert identity.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowReviewResponseBlockageEvent.source_sequence. Original response alert sequence within this inbox.
   */
  source_sequence: number;
  /**
   * OperationWorkflowReviewResponseBlockageEvent.checked_at. Case observation time.
   */
  checked_at: string;
  /**
   * OperationWorkflowReviewResponseBlockageEvent.cleared_blockers. Previously observed blockers positively verified as cleared; unavailable sources and changed failure reasons cannot establish recovery.
   */
  cleared_blockers?: Array<string>;
  /**
   * OperationWorkflowReviewResponseBlockageEvent.added_blockers. Newly observed blockers labeled by fixed linked review position; first observations are silent.
   */
  added_blockers: Array<string>;
};

