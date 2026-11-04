/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationFulfillmentReview } from './OperationWorkflowObligationFulfillmentReview.js';
/**
 * Immutable destination delivery receipt for an unresolved review after its actual overdue reminder and configured grace period. Reassignment after that reminder cancels escalation for that deadline, including reverting an owner change; a new deadline starts a new window. Reaffirmation, reopening, supersession and changed policy/deadline suppress stale delivery. Delivery preserves review ownership and original fulfillment and does not execute application work.
 */
export type OperationWorkflowFulfillmentReviewEscalation = {
  review_id: string;
  notification_id: string;
  reminder_notification_id: string;
  due_at: string;
  escalate_at: string;
  notified_at: string;
  escalation_owner: string;
  review: OperationWorkflowObligationFulfillmentReview;
};

