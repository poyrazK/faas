/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sampled newly added prerequisites, declared checklist evidence issues or unavailable progress for a linked response plan. No business result is inferred.
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
   * OperationWorkflowReviewResponseBlockageEvent.added_blockers. Newly observed blockers labeled by fixed linked review position; first observations are silent.
   */
  added_blockers: Array<string>;
};

