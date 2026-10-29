/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowFulfillmentReviewCoordinationChange } from './OperationWorkflowFulfillmentReviewCoordinationChange.js';
import type { OperationWorkflowFulfillmentReviewEscalation } from './OperationWorkflowFulfillmentReviewEscalation.js';
import type { OperationWorkflowFulfillmentReviewReminder } from './OperationWorkflowFulfillmentReviewReminder.js';
import type { OperationWorkflowNextActionCase } from './OperationWorkflowNextActionCase.js';
import type { OperationWorkflowObligationFulfillment } from './OperationWorkflowObligationFulfillment.js';
import type { OperationWorkflowObligationFulfillmentReview } from './OperationWorkflowObligationFulfillmentReview.js';
import type { OperationWorkflowObligationReaffirmation } from './OperationWorkflowObligationReaffirmation.js';
import type { OperationWorkflowObligationReopening } from './OperationWorkflowObligationReopening.js';
import type { OperationWorkflowObligationReplacement } from './OperationWorkflowObligationReplacement.js';
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Retained source receipt anchored to the saved obligation; observed_assignment describes the delivered assignment snapshot, not a newly audited write. Available event coverage is partial.
 */
export type OperationWorkflowObligationEvent = {
  fulfillment_review_escalation?: OperationWorkflowFulfillmentReviewEscalation;
  fulfillment_review_reminder?: OperationWorkflowFulfillmentReviewReminder;
  review_coordination?: OperationWorkflowFulfillmentReviewCoordinationChange;
  fulfillment_review?: OperationWorkflowObligationFulfillmentReview;
  reaffirmation?: OperationWorkflowObligationReaffirmation;
  reopening?: OperationWorkflowObligationReopening;
  fulfillment?: OperationWorkflowObligationFulfillment;
  target_binding?: OperationWorkflowObligationTargetBinding;
  replacement?: OperationWorkflowObligationReplacement;
  /**
   * Affected case identities captured at the source change. Legacy receipts may omit them; case views exclude unscoped receipts.
   */
  cases?: Array<OperationWorkflowNextActionCase>;
  action_kind?: string;
  shared_plan?: boolean;
  /**
   * Exact obligation identity for mutation-time receipts; older inferred observations may omit it.
   */
  obligation_id?: string;
  kind: 'observed_assignment' | 'accepted' | 'declined' | 'reassigned' | 'completed' | 'canceled' | 'rescheduled' | 'assigned' | 'changed' | 'superseded' | 'replaces' | 'replaced_by' | 'target_bound' | 'target_cleared' | 'fulfilled' | 'reopened' | 'fulfillment_review_required' | 'fulfillment_reaffirmed' | 'fulfillment_review_coordinated' | 'fulfillment_review_overdue' | 'fulfillment_review_escalated';
  at: string;
  actor?: string;
  explanation: string;
};

