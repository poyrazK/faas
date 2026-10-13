/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseDecision } from './OperationWorkflowReviewResponseDecision.js';
import type { OperationWorkflowReviewResponseHandoffAcceptance } from './OperationWorkflowReviewResponseHandoffAcceptance.js';
/**
 * OperationWorkflowReviewResponseChange. Authenticated response update snapshot.
 */
export type OperationWorkflowReviewResponseChange = {
  /**
   * OperationWorkflowReviewResponseChange.handoff_accept_after_seconds. Future handoff acceptance window, defaulting to 3600 seconds. Request omission preserves current policy; active acceptance deadlines stay pinned.
   */
  handoff_accept_after_seconds?: number;
  /**
   * OperationWorkflowReviewResponseChange.handoff_acceptance. Explicit acceptance for the current handed-off review, separate from notification acknowledgment and decision revalidation.
   */
  handoff_acceptance?: OperationWorkflowReviewResponseHandoffAcceptance;
  /**
   * OperationWorkflowReviewResponseChange.decision_review_fallback_owner. Optional distinct fallback team for future overdue decision reviews. Request omission preserves policy and empty string clears it; active episodes retain their pinned fallback.
   */
  decision_review_fallback_owner?: string;
  /**
   * OperationWorkflowReviewResponseChange.decision_review_after_seconds. Future decision revalidation window; defaults to 86400 seconds. Existing deadlines remain pinned; request omission preserves current policy.
   */
  decision_review_after_seconds?: number;
  /**
   * OperationWorkflowReviewResponseChange.decision. Explicit human decision with reviewed condition and accountable reason.
   */
  decision?: OperationWorkflowReviewResponseDecision;
  /**
   * OperationWorkflowReviewResponseChange.fallback_owner. Fallback policy captured for this response change.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReviewResponseChange.reason. Explicit human edit or periodic missed-check-in event; absent in older history.
   */
  reason?: 'human_update' | 'check_in_overdue' | 'decision_review_overdue' | 'handoff_accepted';
  /**
   * OperationWorkflowReviewResponseChange.owner. Responsible team label; trimmed control-free UTF-8 limited to 256 bytes.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponseChange.plan. Human response plan; nonblank UTF-8 limited to 1024 bytes. Line breaks and tabs are allowed.
   */
  plan: string;
  /**
   * OperationWorkflowReviewResponseChange.status. Active ownership or human resolution after a fresh scoped check of linked source reviews.
   */
  status: 'active' | 'resolved';
  /**
   * OperationWorkflowReviewResponseChange.check_in_at. Required future check-in for active responses; absent when resolved. Replaces inbox attention time without changing the original alert response deadline.
   */
  check_in_at?: string;
  /**
   * OperationWorkflowReviewResponseChange.at. Server change time.
   */
  at: string;
  /**
   * OperationWorkflowReviewResponseChange.actor. Authenticated response editor.
   */
  actor: string;
};

