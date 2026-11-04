/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseHandoffReassignment } from './OperationWorkflowReviewResponseHandoffReassignment.js';
/**
 * Historical handoff notice to the assigned team. Check current ownership before accepting; the original review deadline remains unchanged.
 */
export type OperationWorkflowReviewResponseHandoffReassignmentEvent = {
  /**
   * OperationWorkflowReviewResponseHandoffReassignmentEvent.source_notification_id. Original response alert identity.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignmentEvent.source_sequence. Original response sequence in this inbox.
   */
  source_sequence: number;
  /**
   * OperationWorkflowReviewResponseHandoffReassignmentEvent.reassignment. Immutable routing and previous decline context.
   */
  reassignment: OperationWorkflowReviewResponseHandoffReassignment;
};

