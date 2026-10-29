/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseHandoffDecline } from './OperationWorkflowReviewResponseHandoffDecline.js';
/**
 * Immutable manual routing record for an authorized operator, with preserved decline context and one-time notification intent. Ownership assignment is not acceptance.
 */
export type OperationWorkflowReviewResponseHandoffReassignment = {
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.decision_at. Original reviewed decision timestamp.
   */
  decision_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.required_at. Original review requirement timestamp.
   */
  required_at: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.at. Server handoff time, starting the new acceptance clock.
   */
  at: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.notified_at. One-time notification marker, bounded by retained history and inbox.
   */
  notified_at?: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.owner. New responsible team label; membership is not verified.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.previous_owner. Declining owner before this manual transfer.
   */
  previous_owner: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.actor. Authenticated operator performing reassignment.
   */
  actor: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.reason. Operator reason for choosing the new team.
   */
  reason: string;
  /**
   * OperationWorkflowReviewResponseHandoffReassignment.decline. Immutable decline context preserved at reassignment.
   */
  decline: OperationWorkflowReviewResponseHandoffDecline;
};

