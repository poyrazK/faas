/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable acceptance by an authenticated authorized operator for the responsible team, without verifying team membership or satisfying the original review deadline.
 */
export type OperationWorkflowReviewResponseHandoffAcceptance = {
  /**
   * OperationWorkflowReviewResponseHandoffAcceptance.owner. Responsible handed-off team at acceptance time.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponseHandoffAcceptance.actor. Authenticated operator who recorded acceptance.
   */
  actor: string;
  /**
   * OperationWorkflowReviewResponseHandoffAcceptance.at. Server acceptance time.
   */
  at: string;
  /**
   * OperationWorkflowReviewResponseHandoffAcceptance.note. Operator acceptance note.
   */
  note: string;
  /**
   * OperationWorkflowReviewResponseHandoffAcceptance.check_in_at. Future check-in committed at acceptance; later edits do not rewrite this record.
   */
  check_in_at: string;
};

