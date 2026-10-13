/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowFulfillmentReviewCoordinationChange } from './OperationWorkflowFulfillmentReviewCoordinationChange.js';
import type { OperationWorkflowFulfillmentReviewEscalation } from './OperationWorkflowFulfillmentReviewEscalation.js';
import type { OperationWorkflowFulfillmentReviewReminder } from './OperationWorkflowFulfillmentReviewReminder.js';
import type { OperationWorkflowObligationFulfillment } from './OperationWorkflowObligationFulfillment.js';
import type { OperationWorkflowObligationFulfillmentReview } from './OperationWorkflowObligationFulfillmentReview.js';
import type { OperationWorkflowObligationReaffirmation } from './OperationWorkflowObligationReaffirmation.js';
import type { OperationWorkflowObligationReopening } from './OperationWorkflowObligationReopening.js';
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
import type { OperationWorkflowReadinessCompletion } from './OperationWorkflowReadinessCompletion.js';
/**
 * OperationWorkflowReadinessCoordination. Shared inbox coordination with optimistic revision checks; omission of a follow-up time clears it.
 */
export type OperationWorkflowReadinessCoordination = {
  /**
   * Immutable escalation delivery receipts. One delivery per review ID/deadline across destination/policy edits and restoring deadlines; capacity exhaustion stops further escalation delivery for this retained source.
   */
  fulfillment_review_escalations?: Array<OperationWorkflowFulfillmentReviewEscalation>;
  /**
   * Immutable delivery receipts; each review deadline is notified once, even if restored later. At capacity no further reminders are published for this retained source.
   */
  fulfillment_review_reminders?: Array<OperationWorkflowFulfillmentReviewReminder>;
  /**
   * Immutable review-cycle ownership/deadline changes. Capacity exhaustion rejects further writes without pruning decisions.
   */
  review_coordination_changes?: Array<OperationWorkflowFulfillmentReviewCoordinationChange>;
  fulfillment_reviews?: Array<OperationWorkflowObligationFulfillmentReview>;
  /**
   * Immutable human reaffirmations with fresh application evidence; capacity exhaustion rejects further writes.
   */
  reaffirmations?: Array<OperationWorkflowObligationReaffirmation>;
  /**
   * Retained immutable human reopening receipts. Capacity exhaustion rejects further reopening without pruning prior decisions.
   */
  reopenings?: Array<OperationWorkflowObligationReopening>;
  /**
   * Immutable exact-case cycle fulfillment receipts. Capacity exhaustion rejects further fulfillment; receipts are never pruned into reopened work.
   */
  fulfillments?: Array<OperationWorkflowObligationFulfillment>;
  target_bindings?: Array<OperationWorkflowObligationTargetBinding>;
  completion?: OperationWorkflowReadinessCompletion;
  /**
   * OperationWorkflowReadinessCoordination.revision. Shared coordination value; does not alter application evidence.
   */
  revision: number;
  /**
   * OperationWorkflowReadinessCoordination.status. Shared coordination value; does not alter application evidence.
   */
  status: 'unread' | 'read' | 'acknowledged';
  /**
   * OperationWorkflowReadinessCoordination.owner. Shared coordination value; does not alter application evidence.
   */
  owner: string;
  /**
   * OperationWorkflowReadinessCoordination.note. Shared coordination value; does not alter application evidence.
   */
  note: string;
  /**
   * OperationWorkflowReadinessCoordination.follow_up_at. Shared coordination value; does not alter application evidence.
   */
  follow_up_at?: string;
  /**
   * OperationWorkflowReadinessCoordination.updated_at. Shared coordination value; does not alter application evidence.
   */
  updated_at: string;
  /**
   * OperationWorkflowReadinessCoordination.acknowledged_at. Shared coordination value; does not alter application evidence.
   */
  acknowledged_at?: string;
  /**
   * OperationWorkflowReadinessCoordination.updated_by. Shared coordination value; does not alter application evidence.
   */
  updated_by: string;
  /**
   * OperationWorkflowReadinessCoordination.acknowledged_by. Shared coordination value; does not alter application evidence.
   */
  acknowledged_by?: string;
};

