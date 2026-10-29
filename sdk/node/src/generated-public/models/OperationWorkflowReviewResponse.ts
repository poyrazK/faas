/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseChange } from './OperationWorkflowReviewResponseChange.js';
import type { OperationWorkflowReviewResponseDecision } from './OperationWorkflowReviewResponseDecision.js';
import type { OperationWorkflowReviewResponseDecisionReview } from './OperationWorkflowReviewResponseDecisionReview.js';
import type { OperationWorkflowReviewResponseHealth } from './OperationWorkflowReviewResponseHealth.js';
import type { OperationWorkflowReviewResponseLink } from './OperationWorkflowReviewResponseLink.js';
/**
 * OperationWorkflowReviewResponse. Human accountability record under the notification coordination revision fence, independent of business workflow success.
 */
export type OperationWorkflowReviewResponse = {
  /**
   * OperationWorkflowReviewResponse.handoff_acceptance_escalation_owner. Optional control-free UTF-8 team label for future missed acceptance deadlines. Omission preserves policy, empty string clears it; the destination is pinned at each actual handoff. Must differ from the parent request owner when explicitly configured.
   */
  handoff_acceptance_escalation_owner?: string;
  /**
   * OperationWorkflowReviewResponse.handoff_accept_after_seconds. Future handoff acceptance window, defaulting to 3600 seconds. Request omission preserves current policy; active acceptance deadlines stay pinned.
   */
  handoff_accept_after_seconds?: number;
  /**
   * OperationWorkflowReviewResponse.decision_review_fallback_owner. Optional distinct fallback team for future overdue decision reviews. Request omission preserves policy and empty string clears it; active episodes retain their pinned fallback.
   */
  decision_review_fallback_owner?: string;
  /**
   * OperationWorkflowReviewResponse.decision_review_after_seconds. Future decision revalidation window; defaults to 86400 seconds. Existing deadlines remain pinned; request omission preserves current policy.
   */
  decision_review_after_seconds?: number;
  /**
   * OperationWorkflowReviewResponse.decision_review. Latched review requirement after the condition captured by an explicit decision changed.
   */
  decision_review?: OperationWorkflowReviewResponseDecisionReview;
  /**
   * OperationWorkflowReviewResponse.decision. Explicit human decision with reviewed condition and accountable reason.
   */
  decision?: OperationWorkflowReviewResponseDecision;
  /**
   * OperationWorkflowReviewResponse.health. Latest sampled plan condition with source freshness; preserved as historical context after resolution.
   */
  health?: OperationWorkflowReviewResponseHealth;
  /**
   * OperationWorkflowReviewResponse.fallback_owner. Declared fallback team for overdue response check-ins.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReviewResponse.check_in_initial_owner. Team assigned when this check-in was explicitly scheduled; unknown for legacy windows.
   */
  check_in_initial_owner?: string;
  /**
   * OperationWorkflowReviewResponse.check_in_owner_changed. Ownership changed during this check-in period; prevents automatic takeover even if the owner later reverts.
   */
  check_in_owner_changed?: boolean;
  /**
   * OperationWorkflowReviewResponse.check_in_notified_at. One-time overdue notice marker for the current check-in; cleared only by a newly scheduled active check-in.
   */
  check_in_notified_at?: string;
  /**
   * OperationWorkflowReviewResponse.owner. Responsible team label; trimmed control-free UTF-8 limited to 256 bytes.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponse.plan. Human response plan; nonblank UTF-8 limited to 1024 bytes. Line breaks and tabs are allowed.
   */
  plan: string;
  /**
   * OperationWorkflowReviewResponse.status. Active ownership or human resolution after a fresh scoped check of linked source reviews.
   */
  status: 'active' | 'resolved';
  /**
   * OperationWorkflowReviewResponse.check_in_at. Required future check-in for active responses; absent when resolved. Replaces inbox attention time without changing the original alert response deadline.
   */
  check_in_at?: string;
  /**
   * OperationWorkflowReviewResponse.reviews. Fixed captured review links.
   */
  reviews: Array<OperationWorkflowReviewResponseLink>;
  /**
   * OperationWorkflowReviewResponse.created_at. Server creation time.
   */
  created_at: string;
  /**
   * OperationWorkflowReviewResponse.created_by. Authenticated initial owner-record actor.
   */
  created_by: string;
  /**
   * OperationWorkflowReviewResponse.updated_at. Server latest response update time.
   */
  updated_at: string;
  /**
   * OperationWorkflowReviewResponse.updated_by. Authenticated latest response actor.
   */
  updated_by: string;
  /**
   * OperationWorkflowReviewResponse.resolution_checked_at. Advisory pre-write source check time for a human resolved disposition. Missing or unresolved progress rejects resolution; later regressions do not rewrite historical closure.
   */
  resolution_checked_at?: string;
  /**
   * OperationWorkflowReviewResponse.history. Bounded owner, plan, status and check-in change history.
   */
  history: Array<OperationWorkflowReviewResponseChange>;
  /**
   * OperationWorkflowReviewResponse.history_truncated. Whether earlier response changes were pruned.
   */
  history_truncated: boolean;
};

