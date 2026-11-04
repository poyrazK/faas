/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowActionObligation } from './OperationWorkflowActionObligation.js';
import type { OperationWorkflowObligationFulfillmentReview } from './OperationWorkflowObligationFulfillmentReview.js';
import type { OperationWorkflowReviewResponseLink } from './OperationWorkflowReviewResponseLink.js';
/**
 * One current coordination obligation or suggestion for an exact workflow case, including the responsible label, its own deadline and a source inbox link. Shared plans may span cases; evidence includes only references matching this case. This does not authorize application execution or infer business completion.
 */
export type OperationWorkflowCaseNextAction = {
  /**
   * Pending exact-case review of separately fulfilled work. Has an independent work-item identity and no inferred deadline.
   */
  fulfillment_review?: OperationWorkflowObligationFulfillmentReview;
  obligation?: OperationWorkflowActionObligation;
  kind: 'response_check_in' | 'decision_revalidation' | 'handoff_acceptance' | 'handoff_reassignment' | 'escalation_acknowledgment' | 'coordinator_completion' | 'scheduled_evidence_check' | 'coordinator_evidence_review' | 'coordinator_revision' | 'dependency_review' | 'follow_up' | 'fulfillment_review';
  summary: string;
  owner?: string;
  /**
   * Human obligation, automatic scheduled worker check or optional operator suggestion. Opening controls does not perform the action.
   */
  trigger: 'human' | 'automatic' | 'suggested';
  due_at?: string;
  /**
   * This action's own due time is at or before coordination_checked_at. Undated suggestions are never overdue.
   */
  overdue: boolean;
  subscription_id: string;
  sequence: number;
  evidence?: Array<OperationWorkflowReviewResponseLink>;
  /**
   * The originating plan or completion has references to other workflow cases. Case evidence is filtered; the source controls act on the shared record.
   */
  shared_plan?: boolean;
};

