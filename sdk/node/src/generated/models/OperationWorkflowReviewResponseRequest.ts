/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseDecisionRequest } from './OperationWorkflowReviewResponseDecisionRequest.js';
import type { OperationWorkflowReviewResponseHandoffAcceptanceRequest } from './OperationWorkflowReviewResponseHandoffAcceptanceRequest.js';
import type { OperationWorkflowReviewResponseLinkRequest } from './OperationWorkflowReviewResponseLinkRequest.js';
/**
 * OperationWorkflowReviewResponseRequest. Explicit follow-through on an original critical or overdue business-impact alert. Saving acknowledges the alert and assigns queue attention to the response owner.
 */
export type OperationWorkflowReviewResponseRequest = {
  /**
   * OperationWorkflowReviewResponseRequest.handoff_accept_after_seconds. Future handoff acceptance window, defaulting to 3600 seconds. Request omission preserves current policy; active acceptance deadlines stay pinned.
   */
  handoff_accept_after_seconds?: number;
  /**
   * OperationWorkflowReviewResponseRequest.handoff_acceptance. Explicit acceptance for the current handed-off review, separate from notification acknowledgment and decision revalidation.
   */
  handoff_acceptance?: OperationWorkflowReviewResponseHandoffAcceptanceRequest;
  /**
   * OperationWorkflowReviewResponseRequest.decision_review_fallback_owner. Optional distinct fallback team for future overdue decision reviews. Request omission preserves policy and empty string clears it; active episodes retain their pinned fallback.
   */
  decision_review_fallback_owner?: string;
  /**
   * OperationWorkflowReviewResponseRequest.decision_review_after_seconds. Future decision revalidation window; defaults to 86400 seconds. Existing deadlines remain pinned; request omission preserves current policy.
   */
  decision_review_after_seconds?: number;
  /**
   * OperationWorkflowReviewResponseRequest.decision. Explicit human decision with reviewed condition and accountable reason.
   */
  decision?: OperationWorkflowReviewResponseDecisionRequest;
  /**
   * OperationWorkflowReviewResponseRequest.fallback_owner. Optional distinct team for a missed check-in; omit to preserve existing policy, or send empty to clear it. Explicit ownership changes protect the current check-in from automatic takeover.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReviewResponseRequest.owner. Responsible team label; trimmed control-free UTF-8 limited to 256 bytes.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponseRequest.plan. Human response plan; nonblank UTF-8 limited to 1024 bytes. Line breaks and tabs are allowed.
   */
  plan: string;
  /**
   * OperationWorkflowReviewResponseRequest.status. Active ownership or human resolution after a fresh scoped check of linked source reviews.
   */
  status: 'active' | 'resolved';
  /**
   * OperationWorkflowReviewResponseRequest.check_in_at. Required future check-in for active responses; absent when resolved. Replaces inbox attention time without changing the original alert response deadline.
   */
  check_in_at?: string;
  /**
   * OperationWorkflowReviewResponseRequest.reviews. Required nonempty link set on first creation. Omit on later edits to preserve it; supplied sets must exactly match existing links. Links cannot be dropped to manufacture resolution.
   */
  reviews?: Array<OperationWorkflowReviewResponseLinkRequest>;
};

