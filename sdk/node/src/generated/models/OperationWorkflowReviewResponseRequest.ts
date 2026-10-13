/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseDecisionRequest } from './OperationWorkflowReviewResponseDecisionRequest.js';
import type { OperationWorkflowReviewResponseEscalationAcknowledgmentRequest } from './OperationWorkflowReviewResponseEscalationAcknowledgmentRequest.js';
import type { OperationWorkflowReviewResponseEscalationActionReviewRequest } from './OperationWorkflowReviewResponseEscalationActionReviewRequest.js';
import type { OperationWorkflowReviewResponseEscalationCompletionRequest } from './OperationWorkflowReviewResponseEscalationCompletionRequest.js';
import type { OperationWorkflowReviewResponseEscalationRevisionRequest } from './OperationWorkflowReviewResponseEscalationRevisionRequest.js';
import type { OperationWorkflowReviewResponseHandoffAcceptanceRequest } from './OperationWorkflowReviewResponseHandoffAcceptanceRequest.js';
import type { OperationWorkflowReviewResponseHandoffDeclineRequest } from './OperationWorkflowReviewResponseHandoffDeclineRequest.js';
import type { OperationWorkflowReviewResponseHandoffReassignmentRequest } from './OperationWorkflowReviewResponseHandoffReassignmentRequest.js';
import type { OperationWorkflowReviewResponseLinkRequest } from './OperationWorkflowReviewResponseLinkRequest.js';
import type { OperationWorkflowReviewResponseScheduledReviewCancellationRequest } from './OperationWorkflowReviewResponseScheduledReviewCancellationRequest.js';
import type { OperationWorkflowReviewResponseScheduledReviewRescheduleRequest } from './OperationWorkflowReviewResponseScheduledReviewRescheduleRequest.js';
/**
 * OperationWorkflowReviewResponseRequest. Explicit follow-through on an original critical or overdue business-impact alert. Saving acknowledges the alert and assigns queue attention to the response owner.
 */
export type OperationWorkflowReviewResponseRequest = {
  /**
   * OperationWorkflowReviewResponseRequest.scheduled_review_reschedule. Explicit audited change of a pending evidence check time, preserving completed work and review obligations.
   */
  scheduled_review_reschedule?: OperationWorkflowReviewResponseScheduledReviewRescheduleRequest;
  /**
   * OperationWorkflowReviewResponseRequest.scheduled_review_cancellation. Explicit cancellation of a pending scheduled evidence check with immutable actor, time and reason; manual reviews remain available.
   */
  scheduled_review_cancellation?: OperationWorkflowReviewResponseScheduledReviewCancellationRequest;
  /**
   * OperationWorkflowReviewResponseRequest.escalation_revision. Scoped evidence review for completed coordinator work, separate from business success.
   */
  escalation_revision?: OperationWorkflowReviewResponseEscalationRevisionRequest;
  /**
   * OperationWorkflowReviewResponseRequest.escalation_action_review. Scoped evidence review for completed coordinator work, separate from business success.
   */
  escalation_action_review?: OperationWorkflowReviewResponseEscalationActionReviewRequest;
  /**
   * OperationWorkflowReviewResponseRequest.escalation_completion. Explicit completion of coordinator work, separate from handoff acceptance and business outcomes.
   */
  escalation_completion?: OperationWorkflowReviewResponseEscalationCompletionRequest;
  /**
   * OperationWorkflowReviewResponseRequest.escalation_acknowledgment. Explicit escalation coordination commitment, separate from handoff acceptance and decision revalidation.
   */
  escalation_acknowledgment?: OperationWorkflowReviewResponseEscalationAcknowledgmentRequest;
  /**
   * OperationWorkflowReviewResponseRequest.handoff_acceptance_escalation_owner. Optional control-free UTF-8 team label for future missed acceptance deadlines. Omission preserves policy, empty string clears it; the destination is pinned at each actual handoff. Must differ from the parent request owner when explicitly configured.
   */
  handoff_acceptance_escalation_owner?: string;
  /**
   * OperationWorkflowReviewResponseRequest.handoff_reassignment. Explicit reassignment after decline with accountable actor, reason and new acceptance requirement.
   */
  handoff_reassignment?: OperationWorkflowReviewResponseHandoffReassignmentRequest;
  /**
   * OperationWorkflowReviewResponseRequest.handoff_decline. Explicit immutable decline of the current handoff with accountable actor and reason.
   */
  handoff_decline?: OperationWorkflowReviewResponseHandoffDeclineRequest;
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

