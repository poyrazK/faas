/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationFulfillmentReview } from './OperationWorkflowObligationFulfillmentReview.js';
/**
 * Immutable atomic inbox-delivery receipt for one review work-item and deadline. Includes the saved owner, reasons, case and evidence context. Identical deadlines do not repeat within the same review cycle; a new deadline or review cycle is independently eligible. Delivery does not complete or reopen the original work, change its owner, or assert fresh application evidence.
 */
export type OperationWorkflowFulfillmentReviewReminder = {
  review_id: string;
  notification_id: string;
  due_at: string;
  notified_at: string;
  review: OperationWorkflowObligationFulfillmentReview;
};

